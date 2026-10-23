import {main} from "../wailsjs/go/models";

// 行 row から row + 1 へ向かう線。from / to はレーン番号、lane は色分け用。
// current は今のブランチの履歴をたどる線か
export type Edge = {
    from: number;
    to: number;
    lane: number;
    current: boolean;
};

export type GraphRow = {
    commit: main.Commit;
    lane: number;
    edges: Edge[];
};

export type Graph = {
    rows: GraphRow[];
    laneCount: number;
};

// トポロジカル順のコミット列を、レーン位置と行間の線に割り付ける。
// current は今のブランチ（HEAD から辿れる）コミットのハッシュ
export function layoutGraph(commits: main.Commit[], current: Set<string>): Graph {
    // lanes[j] はレーン j が次に待っているコミットのハッシュ
    const lanes: (string | null)[] = [];
    // laneCurrent[j] はレーン j が今のブランチの履歴をたどる線か
    const laneCurrent: boolean[] = [];
    const rows: GraphRow[] = [];
    let laneCount = 0;

    const freeLane = () => {
        const i = lanes.indexOf(null);
        if (i >= 0) return i;
        lanes.push(null);
        return lanes.length - 1;
    };

    commits.forEach((commit, row) => {
        let lane = lanes.indexOf(commit.hash);
        if (lane < 0) lane = freeLane();
        // このコミットを待っていたレーンはここで合流して閉じる
        for (let j = 0; j < lanes.length; j++) {
            if (lanes[j] === commit.hash) lanes[j] = null;
        }
        // ノードを素通りするレーン
        const passing = lanes.slice();
        const passingCurrent = laneCurrent.slice();
        const isCurrent = current.has(commit.hash);

        // ノードから親へ向かう線の行き先レーン
        const fromNode = new Set<number>();
        commit.parents.forEach((parent, i) => {
            const linked = isCurrent && current.has(parent);
            const existing = lanes.indexOf(parent);
            if (existing >= 0) {
                fromNode.add(existing);
                // 今のブランチがほかの線に合流したら、その先は今のブランチの線として描く
                if (linked) laneCurrent[existing] = true;
                return;
            }
            const j = i === 0 && lanes[lane] === null ? lane : freeLane();
            lanes[j] = parent;
            laneCurrent[j] = linked;
            fromNode.add(j);
        });

        const next = commits[row + 1];
        const edges: Edge[] = [];
        if (next) {
            const nextLane = lanes.indexOf(next.hash);
            lanes.forEach((hash, j) => {
                if (hash === null) return;
                const to = hash === next.hash ? nextLane : j;
                if (passing[j] === hash) edges.push({from: j, to, lane: j, current: passingCurrent[j]});
                if (fromNode.has(j)) edges.push({from: lane, to, lane: j, current: isCurrent && current.has(hash)});
            });
        }

        laneCount = Math.max(laneCount, lanes.length);
        rows.push({commit, lane, edges});
    });

    return {rows, laneCount};
}
