import hidume from './assets/images/hidume.png';
import hidumeQuestion from './assets/images/hidume_question.png';
import hidumeLaugh from './assets/images/hidume_laugh.png';
import hidumeSad from './assets/images/hidume_sad.png';
import {main} from "../wailsjs/go/models";

const CLEAN = '変更はないよ。ピカピカだね';

// エラーを伝えるときは悲しみ、問いかけているときは疑問符を浮かべ、変更がなくきれいなときは喜ぶ
function faceFor(speech: string, sad: boolean) {
    if (sad) return hidumeSad;
    if (speech.endsWith('？')) return hidumeQuestion;
    if (speech === CLEAN) return hidumeLaugh;
    return hidume;
}

export type StatusSpeech = {
    text: string;
    // 吹き出しのボタンで勧める操作
    offer: 'stage' | 'commit' | 'resolve' | null;
};

// 操作がないときに、今の git status についてしゃべる内容
export function describeStatus(entries: main.StatusEntry[]): StatusSpeech {
    if (entries.some((e) => e.index === 'U' || e.workTree === 'U')) {
        return {text: 'コンフリクトしてるファイルがあるよ！ Claude に解消してもらう？', offer: 'resolve'};
    }
    if (entries.length === 0) {
        return {text: CLEAN, offer: null};
    }
    // コミット対象があれば、コミット対象外をコミット対象にするより先にコミットを勧める
    const staged = entries.filter((e) => e.index !== ' ' && e.index !== '?').length;
    if (staged > 0) {
        return {text: `コミット対象が ${staged} 件あるよ。コミットする？`, offer: 'commit'};
    }
    return {text: `コミット対象外が ${entries.length} 件あるよ。コミット対象にする？`, offer: 'stage'};
}

// エラーのときのセリフ
export function describeError(error: string): string {
    if (error.includes('not a git repository')) {
        return 'ここ、git リポジトリじゃないみたい…？';
    }
    return 'エラーが出ちゃった…';
}

// 確定の Enter か。IME の変換を確定する Enter は除く。
// WebKit（Wails の macOS の画面）は確定の Enter を isComposing: false で送ってくるので、keyCode 229 でも見分ける
export function isEnter(e: React.KeyboardEvent) {
    return e.key === 'Enter' && !e.nativeEvent.isComposing && e.keyCode !== 229;
}

export type SpeechAction = {
    label: string;
    onClick: () => void;
    disabled?: boolean;
    // 取り返しのつきにくい操作。赤くして目立たせる
    danger?: boolean;
};

// 複数行で入力し ⌘ / Ctrl + Enter で確定する。singleLine なら 1 行で Enter で確定。Esc でやめる
export type SpeechInput = {
    value: string;
    placeholder: string;
    disabled?: boolean;
    singleLine?: boolean;
    // 複数行のときの高さ。既定は 3 行
    rows?: number;
    onChange: (value: string) => void;
    onSubmit: () => void;
    onCancel: () => void;
};

export type SpeechSelect = {
    label: string;
    value: string;
    options: { value: string; label: string }[];
    onChange: (value: string) => void;
};

export type SpeechOption = {
    label: string;
    checked: boolean;
    onChange: (checked: boolean) => void;
};

export function Mascot({speech, actions, inputs, options, select, loading, sad}: {
    speech: string;
    actions: SpeechAction[];
    // 上から順に並べ、最初の欄にフォーカスする
    inputs: SpeechInput[];
    options: SpeechOption[];
    select?: SpeechSelect;
    // 待っている間、吹き出しにくるくる回る印を出す
    loading?: boolean;
    // エラーを伝えているときは悲しい顔にする
    sad?: boolean;
}) {
    return (
        <>
            <img src={faceFor(speech, !!sad)} className="mascot-image" alt="ヒヅメ"/>
            {/* セリフが変わるたびに吹き出しを作り直して、出てくるアニメーションを流す。
                入力欄を出している間は、作り直さず大きさも固定して、ガタつかないようにする */}
            <div key={inputs.length > 0 ? 'input' : speech}
                 className={`speech-bubble${inputs.length > 0 ? ' has-input' : ''}`}>
                <div className="speech-text">
                    {loading && <span className="spinner" role="status" aria-label="読み込み中"/>}
                    {speech}
                </div>
                {select && (
                    <label className="speech-select">
                        {select.label}
                        <select value={select.value} onChange={(e) => select.onChange(e.target.value)}>
                            {select.options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
                        </select>
                    </label>
                )}
                {inputs.map((input, i) => input.singleLine ? (
                    <input key={i} className="speech-input" autoFocus={i === 0} value={input.value}
                           placeholder={input.placeholder} disabled={input.disabled} spellCheck={false}
                           onChange={(e) => input.onChange(e.target.value)}
                           onKeyDown={(e) => {
                               if (isEnter(e)) input.onSubmit();
                               if (e.key === 'Escape') input.onCancel();
                           }}/>
                ) : (
                    <textarea key={i} className="speech-input" autoFocus={i === 0} rows={input.rows ?? 3}
                              value={input.value} placeholder={input.placeholder} disabled={input.disabled}
                              onChange={(e) => input.onChange(e.target.value)}
                              onKeyDown={(e) => {
                                  if (isEnter(e) && (e.metaKey || e.ctrlKey)) {
                                      input.onSubmit();
                                  }
                                  if (e.key === 'Escape') input.onCancel();
                              }}/>
                ))}
                {options.map((option) => (
                    <label key={option.label} className="speech-option">
                        <input type="checkbox" checked={option.checked}
                               onChange={(e) => option.onChange(e.target.checked)}/>
                        {option.label}
                    </label>
                ))}
                {actions.length > 0 && (
                    <div className="speech-actions">
                        {actions.map((a) => (
                            <button key={a.label} className={a.danger ? 'danger' : undefined} disabled={a.disabled}
                                    onClick={a.onClick}>{a.label}</button>
                        ))}
                    </div>
                )}
            </div>
        </>
    );
}
