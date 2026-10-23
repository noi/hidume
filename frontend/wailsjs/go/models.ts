export namespace main {
	
	export class ChatMessage {
	    role: string;
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new ChatMessage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.text = source["text"];
	    }
	}
	export class ChatState {
	    dir: string;
	    sessionId: string;
	    messages: ChatMessage[];
	    running: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ChatState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dir = source["dir"];
	        this.sessionId = source["sessionId"];
	        this.messages = this.convertValues(source["messages"], ChatMessage);
	        this.running = source["running"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Commit {
	    hash: string;
	    parents: string[];
	    author: string;
	    time: number;
	    refs: string[];
	    subject: string;
	    unpushed: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Commit(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hash = source["hash"];
	        this.parents = source["parents"];
	        this.author = source["author"];
	        this.time = source["time"];
	        this.refs = source["refs"];
	        this.subject = source["subject"];
	        this.unpushed = source["unpushed"];
	    }
	}
	export class ForcePushPlan {
	    upstream: string;
	    expected: string;
	    lost: string[];
	    ahead: number;
	
	    static createFrom(source: any = {}) {
	        return new ForcePushPlan(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.upstream = source["upstream"];
	        this.expected = source["expected"];
	        this.lost = source["lost"];
	        this.ahead = source["ahead"];
	    }
	}
	export class GitHubOwners {
	    user: string;
	    orgs: string[];
	
	    static createFrom(source: any = {}) {
	        return new GitHubOwners(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.user = source["user"];
	        this.orgs = source["orgs"];
	    }
	}
	export class MergeStatus {
	    base: string;
	    behind: number;
	    ahead: number;
	    wouldConflict: string[];
	    merging: boolean;
	    conflicts: string[];
	
	    static createFrom(source: any = {}) {
	        return new MergeStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.base = source["base"];
	        this.behind = source["behind"];
	        this.ahead = source["ahead"];
	        this.wouldConflict = source["wouldConflict"];
	        this.merging = source["merging"];
	        this.conflicts = source["conflicts"];
	    }
	}
	export class PullRequest {
	    number: number;
	    title: string;
	    state: string;
	    isDraft: boolean;
	    url: string;
	    mergeable: string;
	
	    static createFrom(source: any = {}) {
	        return new PullRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.number = source["number"];
	        this.title = source["title"];
	        this.state = source["state"];
	        this.isDraft = source["isDraft"];
	        this.url = source["url"];
	        this.mergeable = source["mergeable"];
	    }
	}
	export class PullRequestDefaults {
	    bases: string[];
	    title: string;
	    body: string;
	
	    static createFrom(source: any = {}) {
	        return new PullRequestDefaults(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.bases = source["bases"];
	        this.title = source["title"];
	        this.body = source["body"];
	    }
	}
	export class PushStatus {
	    branch: string;
	    hasOrigin: boolean;
	    upstream: string;
	    ahead: number;
	    behind: number;
	    defaultBranch: string;
	
	    static createFrom(source: any = {}) {
	        return new PushStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.branch = source["branch"];
	        this.hasOrigin = source["hasOrigin"];
	        this.upstream = source["upstream"];
	        this.ahead = source["ahead"];
	        this.behind = source["behind"];
	        this.defaultBranch = source["defaultBranch"];
	    }
	}
	export class RepoSelection {
	    repo: string;
	    needsInit: string;
	
	    static createFrom(source: any = {}) {
	        return new RepoSelection(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.repo = source["repo"];
	        this.needsInit = source["needsInit"];
	    }
	}
	export class SetupTool {
	    name: string;
	    installed: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SetupTool(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.installed = source["installed"];
	    }
	}
	export class SetupStatus {
	    done: boolean;
	    tools: SetupTool[];
	
	    static createFrom(source: any = {}) {
	        return new SetupStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.done = source["done"];
	        this.tools = this.convertValues(source["tools"], SetupTool);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class StatusEntry {
	    index: string;
	    workTree: string;
	    path: string;
	    origPath: string;
	
	    static createFrom(source: any = {}) {
	        return new StatusEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.workTree = source["workTree"];
	        this.path = source["path"];
	        this.origPath = source["origPath"];
	    }
	}
	export class Worktree {
	    path: string;
	    branch: string;
	    isMain: boolean;
	    current: boolean;
	    managed: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Worktree(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.branch = source["branch"];
	        this.isMain = source["isMain"];
	        this.current = source["current"];
	        this.managed = source["managed"];
	    }
	}

}

