import {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import './App.css';
import {
    AbortMerge,
    Commit,
    ContinueMerge,
    CommitAll,
    CreateBranch,
    CreateInitialCommit,
    CreateGitHubRepository,
    CreatePullRequest,
    FetchBase,
    GenerateCommitMessage,
    GetBranchPrefix,
    GetCommits,
    GetGitHubOwners,
    GetMergeStatus,
    GetPullRequest,
    GetPullRequestDefaults,
    GetPushStatus,
    GetRepository,
    GetStatus,
    InitRepository,
    ListWorktrees,
    MergeBase,
    OpenURL,
    Push,
    RevertTo,
    SelectRepository,
    SendChat,
    SetOrigin,
    SwitchWorktree,
    Stage,
    StageAll,
    Unstage
} from "../wailsjs/go/main/App";
import {main} from "../wailsjs/go/models";
import {EventsOn} from "../wailsjs/runtime/runtime";
import {layoutGraph} from "./commitGraph";
import {ChatPane} from "./ChatPane";
import {describeError, describeStatus, Mascot, SpeechAction, SpeechInput, SpeechOption, SpeechSelect} from "./Mascot";

const COMMIT_LIMIT = 300;
// PR の状態は GitHub に聞くので、ファイルの変化とは別にこの間隔で確認する
const PULL_REQUEST_POLL_MS = 90_000;
// Go 側がリポジトリの変化を見つけたときに送ってくるイベント
const REPO_CHANGED_EVENT = 'repo-changed';
// 操作に反応したセリフを出しておく時間
const SPEECH_MS = 4000;
const ROW_HEIGHT = 28;
const LANE_WIDTH = 16;
const NODE_RADIUS = 4;
const LANE_COLORS = ["#4fc3f7", "#ffb74d", "#81c784", "#f06292", "#ba68c8", "#fff176", "#4db6ac", "#e57373"];

// 今のブランチ以外のコミットと線は、この目立たない色で描く
const OTHER_BRANCH_COLOR = '#4a5668';
const BACKGROUND_COLOR = '#1b2636';

const laneColor = (lane: number) => LANE_COLORS[lane % LANE_COLORS.length];
const laneX = (lane: number) => LANE_WIDTH / 2 + lane * LANE_WIDTH;
const rowY = (row: number) => ROW_HEIGHT / 2 + row * ROW_HEIGHT;
const baseName = (path: string) => path.split('/').pop();

// コミットメッセージの入力中の状態。all は吹き出しから全ファイルをコミットするとき
type CommitDraft = {
    all: boolean;
    message: string;
    // Claude が書いたメッセージが入っているか
    byClaude: boolean;
};

// push の確認と、origin がないときの設定の流れ
type PushFlow =
    | { step: 'confirm' }
    | { step: 'noOrigin' }
    | { step: 'url'; value: string }
    // owners は作成先の候補（Organization が先、最後に個人）。取得中は null
    | {
    step: 'create';
    value: string;
    owner: string;
    user: string;
    owners: { value: string; label: string }[] | null;
    // 公開で作る前の、念押しの確認中か
    confirmPublic?: boolean;
};

// 作業用ブランチを作るときの入力。prefix（{user_id}）は取得中は null
type BranchDraft = {
    prefix: string | null;
    name: string;
    // 今の作業中の変更を新しいワークツリーに持っていくか
    carry: boolean;
};

// PR を作るときの入力。bases は取得中は null
type PullRequestDraft = {
    bases: string[] | null;
    base: string;
    title: string;
    draft: boolean;
};

// 右上に出す PR の状態。loading 中は value を前回のまま残す
type PullRequestState = {
    loading: boolean;
    value: main.PullRequest | null;
    error: string;
};

const AUTO_CLAUDE_KEY = 'hidume.autoClaude';

// 「常に Claude に書かせる」はこの端末だけの設定なので localStorage に置く
function loadAutoClaude() {
    try {
        return localStorage.getItem(AUTO_CLAUDE_KEY) === '1';
    } catch {
        return false;
    }
}

function saveAutoClaude(on: boolean) {
    try {
        localStorage.setItem(AUTO_CLAUDE_KEY, on ? '1' : '0');
    } catch {
        // 保存できなくても、このセッション中は効く
    }
}

function App() {
    const [status, setStatus] = useState<main.StatusEntry[]>([]);
    const [commits, setCommits] = useState<main.Commit[]>([]);
    const [repo, setRepo] = useState('');
    const [error, setError] = useState('');
    const [reaction, setReaction] = useState<string | null>(null);
    // git init するか聞いているディレクトリ
    const [initDir, setInitDir] = useState<string | null>(null);
    const [draft, setDraft] = useState<CommitDraft | null>(null);
    const [autoClaude, setAutoClaude] = useState(loadAutoClaude);
    const [generating, setGenerating] = useState(false);
    // 古い生成結果で、開き直した入力欄を上書きしないための番号
    const generation = useRef(0);
    // コミットツリーで選んだコミット
    const [pickedCommit, setPickedCommit] = useState<string | null>(null);
    // ヒヅメくんがリバートしてよいか聞いているところか
    const [confirmingRevert, setConfirmingRevert] = useState(false);
    const [pushStatus, setPushStatus] = useState<main.PushStatus | null>(null);
    const [pushFlow, setPushFlow] = useState<PushFlow | null>(null);
    const [pullRequest, setPullRequest] = useState<PullRequestState>({loading: true, value: null, error: ''});
    const [prDraft, setPrDraft] = useState<PullRequestDraft | null>(null);
    const [branchDraft, setBranchDraft] = useState<BranchDraft | null>(null);
    const [mergeStatus, setMergeStatus] = useState<main.MergeStatus | null>(null);
    // ベースブランチを取り込むか確認しているところか
    const [confirmingMerge, setConfirmingMerge] = useState(false);
    // コンフリクトの解消を Claude パネルに頼んだか
    const [delegatedMerge, setDelegatedMerge] = useState(false);
    // push など時間のかかる操作の最中に、ヒヅメくんが言うこと
    const [busy, setBusy] = useState<string | null>(null);

    useEffect(() => {
        if (!reaction) return;
        const timer = setTimeout(() => setReaction(null), SPEECH_MS);
        return () => clearTimeout(timer);
    }, [reaction]);

    // PR はネットワーク越しで遅いので、ほかの表示を待たせずに後から埋める
    const loadPullRequest = useCallback(() => {
        setPullRequest((p) => ({...p, loading: true}));
        GetPullRequest()
            .then((value) => setPullRequest({loading: false, value: value ?? null, error: ''}))
            .catch((e) => setPullRequest({loading: false, value: null, error: String(e)}));
    }, []);

    // ローカルの情報（変更ファイル、コミット、push の状態）だけを読み直す
    const refreshLocal = useCallback(() => {
        Promise.all([GetStatus(), GetCommits(COMMIT_LIMIT), GetPushStatus(), GetMergeStatus()])
            .then(([s, c, p, m]) => {
                setStatus(s);
                setCommits(c);
                setPushStatus(p);
                setMergeStatus(m);
                if (!m.merging) setDelegatedMerge(false);
                setError('');
            })
            .catch((e) => setError(String(e)));
    }, []);

    const refresh = useCallback(() => {
        refreshLocal();
        loadPullRequest();
    }, [refreshLocal, loadPullRequest]);

    const onError = (e: unknown) => setError(String(e));

    const switchTo = (dir: string, message: string) => {
        setRepo(dir);
        setPickedCommit(null);
        setConfirmingRevert(false);
        setPushFlow(null);
        setPrDraft(null);
        setBranchDraft(null);
        setReaction(message);
        refresh();
    };

    const selectRepository = () => {
        SelectRepository()
            .then(({repo: dir, needsInit}) => {
                setInitDir(needsInit || null);
                if (!needsInit && dir !== repo) switchTo(dir, `${baseName(dir)} を開いたよ`);
            })
            .catch(onError);
    };

    const initRepository = (dir: string) => {
        setInitDir(null);
        InitRepository(dir)
            .then((root) => switchTo(root, `${baseName(root)} で git init したよ！`))
            .catch(onError);
    };

    const stageAll = () => {
        const count = status.filter((e) => e.workTree !== ' ').length;
        StageAll()
            .then(() => setReaction(`${count} 件をコミット対象にしたよ！`))
            .catch(onError)
            .finally(refresh);
    };

    const stagedCount = status.filter((e) => e.index !== ' ' && e.index !== '?').length;
    const branch = pushStatus?.branch ?? '';
    // コミットがまだない（空のリポジトリ）
    const unborn = !!pushStatus && commits.length === 0;
    // デフォルトブランチでは作業しない
    const onDefault = !unborn && !!branch && branch === pushStatus?.defaultBranch;

    const startBranch = () => {
        setBranchDraft({prefix: null, name: '', carry: true});
        GetBranchPrefix()
            .then((prefix) => setBranchDraft((d) => d && {...d, prefix}))
            .catch((e) => {
                setBranchDraft(null);
                onError(e);
            });
    };

    // ブランチは hidume が管理するディレクトリにワークツリーとして作り、そちらを開く
    const switchWorktree = (w: main.Worktree) => {
        if (w.current) return;
        SwitchWorktree(w.path)
            .then((dir) => switchTo(dir, `${w.branch || baseName(w.path)} に切り替えたよ`))
            .catch(onError);
    };

    const createBranch = (d: BranchDraft) => {
        const name = d.name.trim();
        if (!name) return;
        setBranchDraft(null);
        setBusy('ワークツリーを作ってるよ…');
        CreateBranch(name, onDefault && d.carry && status.length > 0)
            .then((dir) => switchTo(dir, `${d.prefix}/${name} のワークツリーを作って開いたよ！`))
            .catch((e) => {
                onError(e);
                refresh();
            })
            .finally(() => setBusy(null));
    };

    // 空のリポジトリには空の Initial commit を作り、そのまま作業用ブランチを作る流れに進む
    const createInitialCommit = () => {
        setBusy('最初のコミットを作ってるよ…');
        CreateInitialCommit()
            .then(startBranch)
            .catch(onError)
            .finally(() => {
                setBusy(null);
                refresh();
            });
    };

    // Claude の書いたメッセージは入力欄に入れるだけで、コミットは人が確かめてから押す
    const writeWithClaude = (all: boolean) => {
        const id = ++generation.current;
        setGenerating(true);
        GenerateCommitMessage(all)
            .then((message) => {
                if (id !== generation.current) return;
                setDraft((d) => d && {...d, message, byClaude: true});
            })
            .catch((e) => {
                if (id === generation.current) onError(e);
            })
            .finally(() => {
                if (id === generation.current) setGenerating(false);
            });
    };

    const openDraft = (all: boolean) => {
        setDraft({all, message: '', byClaude: false});
        if (autoClaude) writeWithClaude(all);
        else setGenerating(false);
    };

    const closeDraft = () => {
        generation.current++;
        setGenerating(false);
        setDraft(null);
    };

    const commit = () => {
        const message = draft?.message.trim();
        if (!draft || !message || generating) return;
        (draft.all ? CommitAll(message) : Commit(message))
            .then(() => {
                closeDraft();
                setPickedCommit(null);
                setReaction('コミットしたよ！');
            })
            .catch(onError)
            .finally(refresh);
    };

    const revert = useMemo(() => planRevert(commits, pickedCommit), [commits, pickedCommit]);

    const runRevert = () => {
        setConfirmingRevert(false);
        if (!revert.target) return;
        const short = revert.target.slice(0, 7);
        RevertTo(revert.target)
            .then(() => {
                setPickedCommit(null);
                setReaction(`${short} の状態に戻したよ！`);
            })
            .catch(onError)
            .finally(refresh);
    };

    // 時間のかかる操作を順に走らせ、その間はヒヅメくんに待っていることを言わせる
    const runBusy = (steps: [string, () => Promise<void>][], done: string) => {
        setPushFlow(null);
        steps.reduce((p, [message, step]) => p.then(() => {
            setBusy(message);
            return step();
        }), Promise.resolve())
            .then(() => setReaction(done))
            .catch(onError)
            .finally(() => {
                setBusy(null);
                refresh();
            });
    };

    const pushing: [string, () => Promise<void>] = ['プッシュしてるよ…', Push];

    // 作成先は Organization を優先し、既定でも最初の Organization を選んでおく
    const startCreate = () => {
        const value = baseName(repo) ?? '';
        setPushFlow({step: 'create', value, owner: '', user: '', owners: null});
        GetGitHubOwners()
            .then(({user, orgs}) => {
                const owners = [
                    ...orgs.map((o) => ({value: o, label: o})),
                    {value: user, label: `${user}（個人）`},
                ];
                setPushFlow((f) => f?.step === 'create'
                    ? {...f, owner: owners[0].value, user, owners}
                    : f);
            })
            .catch((e) => {
                setPushFlow(null);
                onError(e);
            });
    };

    const startPullRequest = () => {
        setPrDraft({bases: null, base: '', title: '', draft: false});
        GetPullRequestDefaults()
            .then(({bases, title}) => setPrDraft((d) => d && {...d, bases, base: bases[0], title}))
            .catch((e) => {
                setPrDraft(null);
                onError(e);
            });
    };

    const createPullRequest = (d: PullRequestDraft) => {
        const title = d.title.trim();
        if (!title) return;
        setPrDraft(null);
        setBusy('PR を作ってるよ…');
        CreatePullRequest(d.base, title, d.draft)
            .then(() => setReaction(d.draft ? '下書きの PR を作ったよ！' : 'PR を作ったよ！'))
            .catch(onError)
            .finally(() => {
                setBusy(null);
                refresh();
            });
    };

    const startPush = () => setPushFlow(pushStatus?.hasOrigin ? {step: 'confirm'} : {step: 'noOrigin'});

    const canPush = !!pushStatus?.branch && commits.length > 0 && !busy
        && !(pushStatus.upstream && pushStatus.ahead === 0);
    const pushTitle = !pushStatus?.branch ? 'ブランチにいないのでプッシュできません'
        : pushStatus.upstream && pushStatus.ahead === 0 ? 'プッシュするコミットはありません'
            : 'プッシュする';

    const base = mergeStatus?.base ?? '';
    const conflicts = mergeStatus?.conflicts ?? [];
    // 取り込み済みかどうかは最後に fetch した時点で判断する（fetch は定期的とウィンドウに戻ったときに走る）
    const upToDate = (mergeStatus?.behind ?? 0) === 0;
    const canMerge = !!base && !onDefault && !unborn && !busy && !mergeStatus?.merging && !upToDate;
    const mergeTitle = !base ? 'origin にベースブランチがありません'
        : onDefault ? 'デフォルトブランチでは取り込みません'
            : mergeStatus?.merging ? 'マージの途中です'
                : upToDate ? `origin/${base} の最新は取り込み済みです`
                    : `origin/${base} の新しいコミット ${mergeStatus?.behind} 件をマージコミットで取り込む`;

    // delegate が true なら、コンフリクトしたときにそのまま Claude パネルへ解消を頼む
    const runMerge = (delegate = false) => {
        setConfirmingMerge(false);
        setBusy(`origin/${base} を取り込んでるよ…`);
        MergeBase()
            .then((conflicted) => {
                if (!conflicted) {
                    setReaction(`origin/${base} を取り込んだよ！`);
                    return;
                }
                // 頼まないときは、マージ中の表示でヒヅメくんがどうするか聞く
                if (delegate) return GetMergeStatus().then((m) => delegateConflicts(m.conflicts, true));
            })
            .catch(onError)
            .finally(() => {
                setBusy(null);
                refresh();
            });
    };

    // コンフリクトの解消は右の Claude パネルに頼む。依頼の文面もそのままパネルに残る
    const delegateConflicts = (files: string[], merging: boolean) => {
        const list = files.map((f) => `- ${f}`).join('\n');
        const situation = merging
            ? `origin/${base} をマージコミットで取り込んでいる途中で、次のファイルがコンフリクトしています。`
            : '次のファイルがコンフリクトしています。';
        const stop = merging
            ? 'git commit、git merge --abort、プッシュはしないでください（マージの完了はこちらでします）。'
            : 'git commit、操作の中止（--abort）、プッシュはしないでください。';
        return SendChat(`${situation}

${list}

それぞれの <<<<<<< ======= >>>>>>> を、両方の変更の意図が残るように解消してください。
解消したファイルは git add してください。${stop}
最後に、どのファイルをどう解消したかを短く教えてください。`)
            .then(() => setDelegatedMerge(true))
            .catch(onError);
    };

    const abortMerge = () => {
        AbortMerge()
            .then(() => setReaction('取り込みをやめて、元に戻したよ'))
            .catch(onError)
            .finally(refresh);
    };

    const continueMerge = () => {
        ContinueMerge()
            .then(() => setReaction(`origin/${base} の取り込みを完了したよ！`))
            .catch(onError)
            .finally(refresh);
    };

    // ヒヅメくんの質問に答えて押すボタンなので、ここからはもう一度確認せずに実行する
    const pushNow = () => pushStatus?.hasOrigin
        ? runBusy([pushing], 'プッシュしたよ！')
        : setPushFlow({step: 'noOrigin'});

    // 作業中の変更がないときに勧める、次の一手
    const nextStep = (): { text: string; actions: SpeechAction[] } | null => {
        if (!pushStatus?.branch || onDefault || unborn) return null;
        const pr = pullRequest.value;
        const behind = mergeStatus?.behind ?? 0;
        const wouldConflict = mergeStatus?.wouldConflict.length ?? 0;
        const merge = {label: '取り込む', onClick: () => runMerge()};
        // コンフリクトしそうなときは、取り込みから Claude への解消の依頼までを一度に進められるようにする
        const resolve = [
            {label: '取り込んで Claude におまかせ', onClick: () => runMerge(true)},
            {label: '取り込むだけ', onClick: () => runMerge()},
        ];

        if (base && wouldConflict > 0) {
            return {
                text: `origin/${base} に新しいコミットがあって、${wouldConflict} 件のファイルがコンフリクトしそう。先に取り込んで解消する？`,
                actions: resolve,
            };
        }
        if (!pushStatus.upstream || pushStatus.ahead > 0) {
            return {
                text: pushStatus.upstream
                    ? `まだプッシュしてないコミットが ${pushStatus.ahead} 件あるよ。プッシュする？`
                    : `${pushStatus.branch} はまだプッシュしてないよ。プッシュする？`,
                actions: [{label: 'プッシュする', onClick: pushNow}],
            };
        }
        if (!pullRequest.loading && !pullRequest.error && (!pr || pr.state === 'CLOSED')) {
            return {
                text: 'プッシュ済みだよ。PR を作る？',
                actions: [{label: 'PR を作る', onClick: startPullRequest}],
            };
        }
        if (pr?.state === 'OPEN' && pr.mergeable === 'CONFLICTING' && base) {
            return {
                text: `PR #${pr.number} がコンフリクトしてるよ。origin/${base} を取り込んで解消する？`,
                actions: resolve,
            };
        }
        if (pr?.state === 'MERGED') {
            return {
                text: `PR #${pr.number} はマージされたよ！ 次の作業用ブランチを作る？`,
                actions: [{label: 'ブランチを作る', onClick: startBranch}],
            };
        }
        if (base && behind > 0) {
            return {
                text: `origin/${base} に新しいコミットが ${behind} 件あるよ。取り込む？`,
                actions: [merge],
            };
        }
        if (pr?.state === 'OPEN') {
            return {
                text: `PR #${pr.number} を出してあるよ。ほかにやることはないよ`,
                actions: [{label: 'PR を開く', onClick: () => OpenURL(pr.url).catch(onError)}],
            };
        }
        return null;
    };

    // 質問とメッセージ入力は、答えるまで出し続ける
    let speech: string;
    let actions: SpeechAction[] = [];
    let input: SpeechInput | undefined;
    let option: SpeechOption | undefined;
    let select: SpeechSelect | undefined;
    if (initDir) {
        speech = `${baseName(initDir)} は git リポジトリじゃないよ。git init する？`;
        actions = [
            {label: 'する', onClick: () => initRepository(initDir)},
            {label: 'しない', onClick: () => setInitDir(null)},
        ];
    } else if (busy) {
        speech = busy;
    } else if (mergeStatus?.merging && conflicts.length > 0 && delegatedMerge) {
        speech = `コンフリクトの解消を Claude にお願いしたよ。右のパネルで様子を見てね（残り ${conflicts.length} 件）`;
        actions = [{label: 'マージをやめる', onClick: abortMerge}];
    } else if (mergeStatus?.merging && conflicts.length > 0) {
        speech = `取り込みの途中で ${conflicts.length} 件のファイルがコンフリクトしたよ！ どうする？`;
        actions = [
            {label: 'Claude におまかせ', onClick: () => delegateConflicts(conflicts, true)},
            {label: 'マージをやめる', onClick: abortMerge},
        ];
    } else if (mergeStatus?.merging) {
        speech = 'コンフリクトは全部解消できたよ。マージを完了する？';
        actions = [
            {label: '完了する', onClick: continueMerge},
            {label: 'マージをやめる', onClick: abortMerge},
        ];
    } else if (confirmingMerge && mergeStatus) {
        const conflictNote = mergeStatus.wouldConflict.length > 0
            ? `${mergeStatus.wouldConflict.length} 件のファイルがコンフリクトしそうだけど、`
            : '';
        speech = `origin/${base} の最新をマージコミットで取り込むよ。${conflictNote}いい？`;
        actions = [
            {label: '取り込む', onClick: () => runMerge()},
            {label: 'やめる', onClick: () => setConfirmingMerge(false)},
        ];
    } else if (pushFlow?.step === 'confirm' && pushStatus) {
        speech = pushStatus.upstream
            ? `${pushStatus.upstream} に ${pushStatus.ahead} 件のコミットをプッシュするよ。いい？`
            : `origin に ${pushStatus.branch} ブランチを新しく作ってプッシュするよ。いい？`;
        actions = [
            {label: 'プッシュする', onClick: () => runBusy([pushing], 'プッシュしたよ！')},
            {label: 'やめる', onClick: () => setPushFlow(null)},
        ];
    } else if (pushFlow?.step === 'noOrigin') {
        speech = 'origin が設定されてないよ。どうする？';
        actions = [
            {label: 'URL を設定する', onClick: () => setPushFlow({step: 'url', value: ''})},
            {label: 'GitHub に作る', onClick: startCreate},
            {label: 'やめる', onClick: () => setPushFlow(null)},
        ];
    } else if (pushFlow?.step === 'url') {
        const url = pushFlow.value.trim();
        const submit = () => url && runBusy([['origin を設定してるよ…', () => SetOrigin(url)], pushing],
            'origin を設定してプッシュしたよ！');
        speech = 'origin にするリポジトリの URL を教えて';
        input = {
            value: pushFlow.value,
            placeholder: 'https://github.com/owner/repo.git',
            singleLine: true,
            onChange: (value) => setPushFlow({step: 'url', value}),
            onSubmit: submit,
            onCancel: () => setPushFlow(null),
        };
        actions = [
            {label: '設定してプッシュ', onClick: submit, disabled: !url},
            {label: 'やめる', onClick: () => setPushFlow(null)},
        ];
    } else if (pushFlow?.step === 'create' && !pushFlow.owners) {
        speech = 'GitHub で作れる場所を調べてるよ…';
        actions = [{label: 'やめる', onClick: () => setPushFlow(null)}];
    } else if (pushFlow?.step === 'create' && pushFlow.confirmPublic) {
        const flow = pushFlow;
        const fullName = `${flow.owner}/${flow.value.trim()}`;
        speech = `${fullName} を公開で作ると、世界中の誰でも中身を見られるようになるよ。`
            + `秘密の情報が入っていないか確かめた？ 本当に公開で作る？`;
        actions = [
            {label: '戻る', onClick: () => setPushFlow({...flow, confirmPublic: false})},
            {
                label: '公開で作る',
                danger: true,
                onClick: () => runBusy([
                    ['GitHub にリポジトリを作ってるよ…',
                        () => CreateGitHubRepository(flow.owner, flow.value.trim(), 'public')],
                    pushing,
                ], `GitHub に ${fullName} を作ってプッシュしたよ！`),
            },
        ];
    } else if (pushFlow?.step === 'create' && pushFlow.owners) {
        const flow = pushFlow;
        const name = flow.value.trim();
        const fullName = `${flow.owner}/${name}`;
        const isOrg = flow.owner !== flow.user;
        const create = (visibility: 'internal' | 'private') => name && runBusy([
            ['GitHub にリポジトリを作ってるよ…', () => CreateGitHubRepository(flow.owner, name, visibility)],
            pushing,
        ], `GitHub に ${fullName} を作ってプッシュしたよ！`);
        speech = 'GitHub のどこに、なんて名前で作る？';
        select = {
            label: '作成先',
            value: flow.owner,
            options: flow.owners!,
            onChange: (owner) => setPushFlow({...flow, owner}),
        };
        input = {
            value: flow.value,
            placeholder: 'リポジトリ名',
            singleLine: true,
            onChange: (value) => setPushFlow({...flow, value}),
            onSubmit: () => create(isOrg ? 'internal' : 'private'),
            onCancel: () => setPushFlow(null),
        };
        // Organization なら社内向けの Internal を先頭（既定）にする。個人には Internal がない
        actions = [
            ...(isOrg ? [{label: '組織限定公開で作る', onClick: () => create('internal'), disabled: !name}] : []),
            {label: '非公開で作る', onClick: () => create('private'), disabled: !name},
            // 公開はすぐには作らず、もう一段確認する
            {label: '公開で作る', onClick: () => setPushFlow({...flow, confirmPublic: true}), disabled: !name},
            {label: 'やめる', onClick: () => setPushFlow(null)},
        ];
    } else if (prDraft && !prDraft.bases) {
        speech = 'PR の準備をしてるよ…';
        actions = [{label: 'やめる', onClick: () => setPrDraft(null)}];
    } else if (prDraft && prDraft.bases) {
        const d = prDraft;
        speech = `${pushStatus?.branch} から PR を作るよ。どこにマージして、タイトルはどうする？`;
        select = {
            label: 'マージ先',
            value: d.base,
            options: d.bases!.map((b) => ({value: b, label: b})),
            onChange: (base) => setPrDraft({...d, base}),
        };
        input = {
            value: d.title,
            placeholder: 'PR のタイトル',
            singleLine: true,
            onChange: (title) => setPrDraft({...d, title}),
            onSubmit: () => createPullRequest(d),
            onCancel: () => setPrDraft(null),
        };
        option = {
            label: '下書き（Draft）で作る',
            checked: d.draft,
            onChange: (draft) => setPrDraft({...d, draft}),
        };
        actions = [
            {label: 'PR を作る', onClick: () => createPullRequest(d), disabled: !d.title.trim()},
            {label: 'やめる', onClick: () => setPrDraft(null)},
        ];
    } else if (confirmingRevert && revert.target) {
        speech = `${revert.target.slice(0, 7)}「${revert.subject}」の状態まで戻すよ。`
            + `後の ${revert.count} 件のコミットを打ち消すコミットを作るけど、いい？`;
        actions = [
            {label: '戻す', onClick: runRevert},
            {label: 'やめる', onClick: () => setConfirmingRevert(false)},
        ];
    } else if (draft) {
        const target = draft.all ? `全部の変更（${status.length} 件）` : `コミット対象（${stagedCount} 件）`;
        if (generating) {
            speech = `${target}のコミットメッセージを Claude に書いてもらってるよ…`;
        } else if (draft.byClaude) {
            speech = `Claude が書いてくれたよ。確かめてからコミットしてね`;
        } else {
            speech = `${target}をコミットするよ。メッセージを教えて`;
        }
        input = {
            value: draft.message,
            placeholder: generating ? 'Claude が書いています…' : 'コミットメッセージ（⌘ + Enter でコミット）',
            disabled: generating,
            onChange: (message) => setDraft({...draft, message}),
            onSubmit: commit,
            onCancel: closeDraft,
        };
        option = {
            label: '常に Claude に書かせる',
            checked: autoClaude,
            onChange: (on) => {
                setAutoClaude(on);
                saveAutoClaude(on);
            },
        };
        actions = [
            {label: 'コミット', onClick: commit, disabled: generating || !draft.message.trim()},
            {label: 'Claude に書かせる', onClick: () => writeWithClaude(draft.all), disabled: generating},
            {label: 'やめる', onClick: closeDraft},
        ];
    } else if (branchDraft && !branchDraft.prefix) {
        speech = 'ブランチ名の準備をしてるよ…';
        actions = [{label: 'やめる', onClick: () => setBranchDraft(null)}];
    } else if (branchDraft && branchDraft.prefix) {
        const d = branchDraft;
        const name = d.name.trim();
        const from = onDefault ? branch : pushStatus?.defaultBranch || 'HEAD';
        speech = `${from} から作業用のブランチをワークツリーで作るよ。名前を教えて（頭に ${d.prefix}/ が付くよ）`;
        input = {
            value: d.name,
            placeholder: '例: add-login',
            singleLine: true,
            onChange: (value) => setBranchDraft({...d, name: value}),
            onSubmit: () => createBranch(d),
            onCancel: () => setBranchDraft(null),
        };
        if (onDefault && status.length > 0) {
            option = {
                label: `今の変更（${status.length} 件）を新しいワークツリーに持っていく`,
                checked: d.carry,
                onChange: (carry) => setBranchDraft({...d, carry}),
            };
        }
        actions = [
            {label: `${d.prefix}/${name || '…'} を作る`, onClick: () => createBranch(d), disabled: !name},
            {label: 'やめる', onClick: () => setBranchDraft(null)},
        ];
    } else if (reaction) {
        speech = reaction;
    } else if (error) {
        speech = describeError(error);
    } else if (unborn && branch) {
        speech = 'まだコミットがないよ。空の最初のコミットを作って、作業用のブランチを切ろうか？';
        actions = [
            {label: '作る', onClick: createInitialCommit},
        ];
    } else if (onDefault) {
        speech = `今は ${branch}（デフォルトブランチ）だよ。ここでは作業しないで、作業用のブランチを作ろうか？`;
        actions = [
            {label: 'ブランチを作る', onClick: startBranch},
        ];
    } else if (status.length > 0) {
        const s = describeStatus(status);
        speech = s.text;
        if (s.offer === 'stage') actions = [{label: 'する', onClick: stageAll}];
        if (s.offer === 'commit') actions = [{label: 'する', onClick: () => openDraft(true)}];
        if (s.offer === 'resolve') {
            const files = status.filter((e) => e.index === 'U' || e.workTree === 'U').map((e) => e.path);
            actions = [{label: 'Claude におまかせ', onClick: () => delegateConflicts(files, false)}];
        }
    } else {
        // 作業中の変更がなければ、push・PR・ベースブランチの取り込みのうち次にやることを案内する
        const next = nextStep();
        speech = next?.text ?? describeStatus(status).text;
        actions = next?.actions ?? [];
    }

    // ローカルの変化は Go 側の監視が知らせてくる。PR は定期的に、ウィンドウに戻ったときにも確認する
    useEffect(() => {
        GetRepository().then(setRepo);
        refresh();
        // ベースブランチも同じ間隔で fetch する。取ってきた変化は Go 側の監視が知らせてくる
        const pollRemote = () => {
            loadPullRequest();
            FetchBase().catch(() => {
                // オフラインなどで取れなくても、前回の情報のまま表示を続ける
            });
        };
        pollRemote();
        const offChanged = EventsOn(REPO_CHANGED_EVENT, refreshLocal);
        const timer = setInterval(pollRemote, PULL_REQUEST_POLL_MS);
        window.addEventListener('focus', pollRemote);
        return () => {
            offChanged();
            clearInterval(timer);
            window.removeEventListener('focus', pollRemote);
        };
    }, [refresh, refreshLocal, loadPullRequest]);

    return (
        <div id="App">
            <header className="mascot">
                <Mascot speech={speech} actions={actions} input={input} option={option} select={select}
                        loading={busy !== null || (draft !== null && generating)
                            || (pushFlow?.step === 'create' && !pushFlow.owners)
                            || (prDraft !== null && !prDraft.bases)
                            || (branchDraft !== null && !branchDraft.prefix)}/>
                <button className="repo" title={repo} onClick={selectRepository}>
                    {repo ? repo.split('/').pop() : 'リポジトリを選択'}
                </button>
                <WorktreeMenu branch={branch || (pushStatus ? 'ブランチなし' : '…')} onDefault={onDefault}
                              defaultBranch={pushStatus?.defaultBranch ?? ''} disabled={unborn || busy !== null}
                              onSwitch={switchWorktree} onCreate={startBranch} onError={onError}/>
            </header>
            {error && <div className="error">{error}</div>}
            <main className="panes">
                <RemotePane push={pushStatus} merge={mergeStatus} pullRequest={pullRequest} busy={busy !== null}
                            onCreatePullRequest={startPullRequest}
                            onOpen={(url) => OpenURL(url).catch(onError)}/>
                <StatusPane key={repo} entries={status} onChange={refresh} onError={onError} onSpeak={setReaction}/>
                <div className="commit-bar">
                    <div className="commit-buttons">
                        <button title={onDefault ? 'デフォルトブランチにはコミットしない' : 'コミット対象をコミットする'}
                                disabled={stagedCount === 0 || onDefault || unborn}
                                onClick={() => openDraft(false)}>→</button>
                    </div>
                </div>
                {/* コミットツリーの上の線に載せ、コミットを上（リモート）へ送るボタン */}
                <div className="push-bar">
                    <button title={pushTitle} disabled={!canPush} onClick={startPush}>↑</button>
                    <button title={mergeTitle} disabled={!canMerge} onClick={() => setConfirmingMerge(true)}>↓</button>
                </div>
                <ChatPane repo={repo} label={branch || baseName(repo) || ''} onError={onError}/>
                <CommitTree commits={commits} selected={pickedCommit} onSelect={setPickedCommit}
                            canRevert={revert.target !== null} onRevert={() => setConfirmingRevert(true)}/>
            </main>
        </div>
    )
}

const isHead = (c: main.Commit) => c.refs.some((r) => r === 'HEAD' || r.startsWith('HEAD -> '));

// 選んだコミットの状態まで戻すときの計画。戻せるのは HEAD から辿れる、HEAD より前のコミットだけ
function planRevert(commits: main.Commit[], picked: string | null) {
    const byHash = new Map(commits.map((c) => [c.hash, c]));
    const head = commits.find(isHead);
    const ancestors = (from: string) => {
        const seen = new Set<string>();
        const stack = [from];
        while (stack.length > 0) {
            const hash = stack.pop()!;
            if (seen.has(hash)) continue;
            seen.add(hash);
            stack.push(...(byHash.get(hash)?.parents ?? []));
        }
        return seen;
    };
    const target = picked ? byHash.get(picked) : undefined;
    if (!head || !target || target.hash === head.hash || !ancestors(head.hash).has(target.hash)) {
        return {target: null, subject: '', count: 0};
    }
    const kept = ancestors(target.hash);
    const count = [...ancestors(head.hash)].filter((h) => !kept.has(h)).length;
    return {target: target.hash, subject: target.subject, count};
}

type StatusFile = {
    path: string;
    code: string;
    label: string;
    // ステージ操作に渡すパス。リネームは元のパスも含める
    targets: string[];
};

type Side = 'unstaged' | 'staged';

const isConflict = (e: main.StatusEntry) => e.index === 'U' || e.workTree === 'U';

function toFile(e: main.StatusEntry, code: string, targets: string[]): StatusFile {
    return {path: e.path, code, label: e.origPath ? `${e.origPath} → ${e.path}` : e.path, targets};
}

function splitStatus(entries: main.StatusEntry[]) {
    const unstaged: StatusFile[] = [];
    const staged: StatusFile[] = [];
    for (const e of entries) {
        if (isConflict(e)) {
            unstaged.push(toFile(e, 'U', [e.path]));
            continue;
        }
        if (e.workTree !== ' ') unstaged.push(toFile(e, e.workTree, [e.path]));
        if (e.index !== ' ' && e.index !== '?') {
            staged.push(toFile(e, e.index, e.origPath ? [e.origPath, e.path] : [e.path]));
        }
    }
    return {unstaged, staged};
}

type Selection = { side: Side; paths: Set<string> };

const allOf = (side: Side, files: StatusFile[]): Selection => ({side, paths: new Set(files.map((f) => f.path))});

// 消えたファイルを選択から外す。選んでいたものが全部消えたら既定の選択に戻す
function resolveSelection(picked: Selection | null, unstaged: StatusFile[], staged: StatusFile[]): Selection {
    if (picked) {
        const files = picked.side === 'unstaged' ? unstaged : staged;
        const paths = new Set(files.map((f) => f.path).filter((p) => picked.paths.has(p)));
        if (paths.size > 0 || picked.paths.size === 0) return {side: picked.side, paths};
    }
    return unstaged.length > 0 ? allOf('unstaged', unstaged) : allOf('staged', staged);
}

function statusKind(code: string) {
    return {'?': 'untracked', U: 'conflict', A: 'added', D: 'deleted', R: 'renamed', C: 'renamed'}[code] ?? 'modified';
}

// コミット対象外・↑↓・コミット対象を、それぞれ .panes のグリッドに直接並べる
function StatusPane({entries, onChange, onError, onSpeak}: {
    entries: main.StatusEntry[];
    onChange: () => void;
    onError: (e: unknown) => void;
    onSpeak: (speech: string) => void;
}) {
    const {unstaged, staged} = useMemo(() => splitStatus(entries), [entries]);
    // null のときは既定の選択（コミット対象外を全部、空ならコミット対象を全部）を使う
    const [picked, setPicked] = useState<Selection | null>(null);
    const selection = useMemo(
        () => resolveSelection(picked, unstaged, staged),
        [picked, unstaged, staged],
    );

    const toggle = (side: Side, path: string) => {
        const paths = new Set(selection.side === side ? selection.paths : []);
        if (paths.has(path)) paths.delete(path); else paths.add(path);
        setPicked({side, paths});
    };

    const move = (side: Side) => {
        const files = side === 'unstaged' ? unstaged : staged;
        const moved = files.filter((f) => selection.paths.has(f.path));
        const targets = moved.flatMap((f) => f.targets);
        if (targets.length === 0) return;
        (side === 'unstaged' ? Stage(targets) : Unstage(targets))
            .then(() => {
                setPicked(null);
                onSpeak(side === 'unstaged'
                    ? `${moved.length} 件をコミット対象にしたよ！`
                    : `${moved.length} 件をコミット対象から外したよ`);
            })
            .catch(onError)
            .finally(onChange);
    };

    const canMove = (side: Side) => selection.side === side && selection.paths.size > 0;

    return (
        <>
            <FileList className="unstaged-pane" title="コミット対象外" files={unstaged} side="unstaged"
                      selection={selection} onToggle={toggle}/>
            <div className="stage-bar">
                <div className="stage-buttons">
                    <button title="コミット対象から外す" disabled={!canMove('staged')} onClick={() => move('staged')}>↑</button>
                    <button title="コミット対象にする" disabled={!canMove('unstaged')} onClick={() => move('unstaged')}>↓</button>
                </div>
            </div>
            <FileList className="staged-pane" title="コミット対象" files={staged} side="staged"
                      selection={selection} onToggle={toggle}/>
        </>
    );
}

function FileList({className, title, files, side, selection, onToggle}: {
    className: string;
    title: string;
    files: StatusFile[];
    side: Side;
    selection: Selection;
    onToggle: (side: Side, path: string) => void;
}) {
    return (
        <section className={`pane ${className}`}>
            <h2>{title} ({files.length})</h2>
            {files.length === 0 ? (
                <p className="empty">変更はありません</p>
            ) : (
                <ul className="status-list">
                    {files.map((f) => {
                        const selected = selection.side === side && selection.paths.has(f.path);
                        return (
                            <li key={f.path} className={`status-${statusKind(f.code)}${selected ? ' selected' : ''}`}
                                onClick={() => onToggle(side, f.path)}>
                                <span className="status-code">{f.code}</span>
                                <span className="status-path" title={f.label}>{f.label}</span>
                            </li>
                        );
                    })}
                </ul>
            )}
        </section>
    );
}

// 今のブランチの表示。押すとワークツリーの一覧を開き、切り替えたり新しく作ったりできる
function WorktreeMenu({branch, onDefault, defaultBranch, disabled, onSwitch, onCreate, onError}: {
    branch: string;
    onDefault: boolean;
    defaultBranch: string;
    disabled: boolean;
    onSwitch: (w: main.Worktree) => void;
    onCreate: () => void;
    onError: (e: unknown) => void;
}) {
    const [worktrees, setWorktrees] = useState<main.Worktree[] | null>(null);
    const ref = useRef<HTMLDivElement>(null);

    // メニューの外を押したら閉じる
    useEffect(() => {
        if (!worktrees) return;
        const close = (e: MouseEvent) => {
            if (!ref.current?.contains(e.target as Node)) setWorktrees(null);
        };
        document.addEventListener('mousedown', close);
        return () => document.removeEventListener('mousedown', close);
    }, [worktrees]);

    const toggle = () => {
        if (worktrees) {
            setWorktrees(null);
            return;
        }
        ListWorktrees().then(setWorktrees).catch(onError);
    };

    const pick = (action: () => void) => {
        setWorktrees(null);
        action();
    };

    return (
        <div className="worktree-menu" ref={ref}>
            <button className={`branch${onDefault ? ' branch-default' : ''}`} disabled={disabled} onClick={toggle}
                    title={onDefault ? 'デフォルトブランチ（ここでは作業しない）。押すとワークツリーを切り替え'
                        : '今のブランチ。押すとワークツリーを切り替え'}>
                {branch}
            </button>
            {worktrees && (
                <ul className="worktree-list">
                    {worktrees.map((w) => (
                        <li key={w.path}>
                            <button className={w.current ? 'current' : undefined} title={w.path}
                                    onClick={() => pick(() => onSwitch(w))}>
                                <span className="worktree-branch">
                                    {w.current ? '✓ ' : ''}{w.branch || '（ブランチなし）'}
                                    {w.isMain && <span className="worktree-tag">本体</span>}
                                    {w.branch === defaultBranch && <span className="worktree-tag">デフォルト</span>}
                                </span>
                                <span className="worktree-path">{w.path}</span>
                            </button>
                        </li>
                    ))}
                    <li>
                        <button className="worktree-create" onClick={() => pick(onCreate)}>＋ 新しいワークツリーを作る</button>
                    </li>
                </ul>
            )}
        </div>
    );
}

const PR_STATE_LABELS: Record<string, string> = {OPEN: 'オープン', MERGED: 'マージ済み', CLOSED: 'クローズ'};

// 右上: ローカルの最新が push 済みかと、今のブランチの PR
function RemotePane({push, merge, pullRequest, busy, onCreatePullRequest, onOpen}: {
    push: main.PushStatus | null;
    merge: main.MergeStatus | null;
    pullRequest: PullRequestState;
    busy: boolean;
    onCreatePullRequest: () => void;
    onOpen: (url: string) => void;
}) {
    let pushLine: { text: string; ok: boolean };
    if (!push?.hasOrigin) {
        pushLine = {text: 'origin が設定されていません', ok: false};
    } else if (!push.branch) {
        pushLine = {text: 'ブランチにいません（HEAD が切り離されています）', ok: false};
    } else if (!push.upstream) {
        pushLine = {text: `${push.branch} はまだプッシュされていません`, ok: false};
    } else if (push.ahead > 0) {
        pushLine = {text: `未プッシュのコミットが ${push.ahead} 件あります`, ok: false};
    } else {
        pushLine = {text: `ローカルの最新は ${push.upstream} にプッシュ済みです`, ok: true};
    }

    const pushed = !!push?.upstream && push.ahead === 0;
    const isDefault = !!push?.branch && push.branch === push.defaultBranch;
    const pr = pullRequest.value;
    // PR がない、またはマージ・クローズ済みなら新しく作れる。デフォルトブランチからは作らない
    const canCreate = !isDefault && pushed && !busy && (!pr || pr.state !== 'OPEN');

    let prLine: React.ReactNode;
    if (!push?.hasOrigin || !push.branch) {
        prLine = null;
    } else if (isDefault) {
        prLine = <span className="remote-muted">デフォルトブランチなので PR は作りません</span>;
    } else if (pullRequest.loading && !pr) {
        prLine = <span className="remote-muted">PR を確認しています…</span>;
    } else if (pullRequest.error) {
        prLine = <span className="remote-muted" title={pullRequest.error}>PR を確認できませんでした</span>;
    } else if (!pr) {
        prLine = <span>PR はまだ作成されていません</span>;
    } else {
        prLine = (
            <button className="pr-link" title={pr.url} onClick={() => onOpen(pr.url)}>
                <span className={`pr-state pr-${pr.isDraft && pr.state === 'OPEN' ? 'draft' : pr.state.toLowerCase()}`}>
                    {pr.isDraft && pr.state === 'OPEN' ? '下書き' : PR_STATE_LABELS[pr.state] ?? pr.state}
                </span>
                {pr.state === 'OPEN' && pr.mergeable === 'CONFLICTING' && (
                    <span className="pr-state pr-conflicting">コンフリクト</span>
                )}
                <span className="pr-title">#{pr.number} {pr.title}</span>
            </button>
        );
    }

    return (
        <section className="pane remote-pane">
            <h2>リモート</h2>
            <p className={pushLine.ok ? 'remote-ok' : undefined}>{pushLine.ok ? '✓ ' : ''}{pushLine.text}</p>
            {push?.upstream && push.behind > 0 && (
                <p className="remote-muted">{push.upstream} にまだ取り込んでいないコミットが {push.behind} 件あります</p>
            )}
            {merge?.merging ? (
                <p className={merge.conflicts.length > 0 ? 'remote-conflict' : undefined}>
                    {merge.conflicts.length > 0
                        ? `⚠ 取り込みの途中で ${merge.conflicts.length} 件のファイルがコンフリクトしています`
                        : 'コンフリクトは解消済みです。マージの完了待ちです'}
                </p>
            ) : merge?.base && !isDefault && merge.behind > 0 && (
                <p className={merge.wouldConflict.length > 0 ? 'remote-conflict' : 'remote-muted'}
                   title={merge.wouldConflict.join('\n') || undefined}>
                    {merge.wouldConflict.length > 0
                        ? `⚠ origin/${merge.base} の新しいコミット ${merge.behind} 件とコンフリクトします（${merge.wouldConflict.length} ファイル）`
                        : `origin/${merge.base} に新しいコミットが ${merge.behind} 件あります（↓ で取り込めます）`}
                </p>
            )}
            {prLine && (
                <div className="remote-pr">
                    {prLine}
                    {!isDefault && (!pr || pr.state !== 'OPEN') && (
                        <button className="pr-create" disabled={!canCreate} onClick={onCreatePullRequest}
                                title={pushed ? 'PR を作る' : '先にプッシュしてね'}>PR を作る</button>
                    )}
                </div>
            )}
        </section>
    );
}

function CommitTree({commits, selected, onSelect, canRevert, onRevert}: {
    commits: main.Commit[];
    selected: string | null;
    onSelect: (hash: string) => void;
    canRevert: boolean;
    onRevert: () => void;
}) {
    // 今のブランチ（HEAD から辿れる）コミット
    const current = useMemo(() => {
        const byHash = new Map(commits.map((c) => [c.hash, c]));
        const set = new Set<string>();
        const stack = commits.filter(isHead).map((c) => c.hash);
        while (stack.length > 0) {
            const hash = stack.pop()!;
            if (set.has(hash)) continue;
            set.add(hash);
            stack.push(...(byHash.get(hash)?.parents ?? []));
        }
        return set;
    }, [commits]);
    const graph = useMemo(() => layoutGraph(commits, current), [commits, current]);
    const width = Math.max(graph.laneCount, 1) * LANE_WIDTH;
    const height = graph.rows.length * ROW_HEIGHT;

    return (
        <section className="tree-pane">
            <div className="pane tree-scroll">
                <h2>
                    コミットツリー
                    <span className="tree-legend">
                        <span><svg width="10" height="10"><circle cx="5" cy="5" r="4" fill="#9aa5b4"/></svg>プッシュ済み</span>
                        <span><svg width="10" height="10"><circle cx="5" cy="5" r="3.5" fill="none" stroke="#9aa5b4" strokeWidth="1.5"/></svg>未プッシュ</span>
                    </span>
                </h2>
                <div className="tree" style={{paddingLeft: width + 8}}>
                    <svg className="graph" width={width} height={height}>
                        {graph.rows.flatMap((r, row) => r.edges.map((e, i) => {
                            const x1 = laneX(e.from), y1 = rowY(row);
                            const x2 = laneX(e.to), y2 = rowY(row + 1);
                            const d = x1 === x2
                                ? `M${x1} ${y1}V${y2}`
                                : `M${x1} ${y1}C${x1} ${y2},${x2} ${y1},${x2} ${y2}`;
                            return <path key={`${row}-${i}`} d={d} fill="none"
                                         stroke={e.current ? laneColor(e.lane) : OTHER_BRANCH_COLOR}
                                         strokeWidth={e.current ? 2 : 1.5}/>;
                        }))}
                        {graph.rows.map((r, row) => (
                            <g key={r.commit.hash}>
                                {/* 未プッシュのコミットは中抜きの円にする */}
                                <circle cx={laneX(r.lane)} cy={rowY(row)} r={NODE_RADIUS}
                                        fill={r.commit.unpushed ? BACKGROUND_COLOR
                                            : current.has(r.commit.hash) ? laneColor(r.lane) : OTHER_BRANCH_COLOR}
                                        stroke={r.commit.unpushed ? laneColor(r.lane) : BACKGROUND_COLOR}
                                        strokeWidth={r.commit.unpushed ? 2 : 1.5}/>
                                {/* 選んでいるコミットはノードを輪で囲む */}
                                {r.commit.hash === selected && (
                                    <circle cx={laneX(r.lane)} cy={rowY(row)} r={NODE_RADIUS + 3.5}
                                            fill="none" stroke="#ffffff" strokeWidth={1.5}/>
                                )}
                            </g>
                        ))}
                    </svg>
                    {graph.rows.map(({commit}) => (
                        <div key={commit.hash}
                             className={`commit-row${current.has(commit.hash) ? '' : ' other-branch'}${commit.hash === selected ? ' selected' : ''}`}
                             style={{height: ROW_HEIGHT}} onClick={() => onSelect(commit.hash)}>
                            {commit.refs.map((ref) => (
                                <span key={ref} className={ref.startsWith('tag: ') ? 'ref ref-tag' : 'ref'}>{ref}</span>
                            ))}
                            <span className="commit-subject" title={commit.subject}>{commit.subject}</span>
                            <span className="commit-meta">{commit.author}</span>
                            <span className="commit-meta">{new Date(commit.time * 1000).toLocaleDateString()}</span>
                            <span className="commit-hash">{commit.hash.slice(0, 7)}</span>
                        </div>
                    ))}
                </div>
            </div>
            {/* スクロールしても右下に残す */}
            <button className="revert-button" disabled={!canRevert}
                    title={canRevert ? '選んだコミットの状態まで戻すコミットを作る' : '戻したいコミットを選んでね'}
                    onClick={onRevert}>リバート</button>
        </section>
    );
}

export default App
