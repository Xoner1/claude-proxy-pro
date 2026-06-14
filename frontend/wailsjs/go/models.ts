export namespace main {
	
	export class Provider {
	    name: string;
	    url: string;
	    key?: string;
	    model: string;
	    priority: number;
	    status: string;
	    last_check: string;
	    requests_today: number;
	    latency_ms: number;
	    success_count: number;
	    fail_count: number;
	
	    static createFrom(source: any = {}) {
	        return new Provider(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.url = source["url"];
	        this.key = source["key"];
	        this.model = source["model"];
	        this.priority = source["priority"];
	        this.status = source["status"];
	        this.last_check = source["last_check"];
	        this.requests_today = source["requests_today"];
	        this.latency_ms = source["latency_ms"];
	        this.success_count = source["success_count"];
	        this.fail_count = source["fail_count"];
	    }
	}
	export class AppConfig {
	    providers: Provider[];
	    port: string;
	    active_idx: number;
	    auto_retry: boolean;
	    retry_max: number;
	    failover: boolean;
	    check_interval_seconds: number;
	
	    static createFrom(source: any = {}) {
	        return new AppConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.providers = this.convertValues(source["providers"], Provider);
	        this.port = source["port"];
	        this.active_idx = source["active_idx"];
	        this.auto_retry = source["auto_retry"];
	        this.retry_max = source["retry_max"];
	        this.failover = source["failover"];
	        this.check_interval_seconds = source["check_interval_seconds"];
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
	export class ModelInfo {
	    id: string;
	    provider: string;
	    context_size: number;
	    pricing: string;
	    latency_ms: number;
	    object: string;
	    display_name: string;
	    created?: number;
	    owned_by?: string;
	
	    static createFrom(source: any = {}) {
	        return new ModelInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.provider = source["provider"];
	        this.context_size = source["context_size"];
	        this.pricing = source["pricing"];
	        this.latency_ms = source["latency_ms"];
	        this.object = source["object"];
	        this.display_name = source["display_name"];
	        this.created = source["created"];
	        this.owned_by = source["owned_by"];
	    }
	}
	
	export class RequestLog {
	    id: number;
	    // Go type: time
	    timestamp: any;
	    provider: string;
	    model: string;
	    stream: boolean;
	    status: number;
	    latency_ms: number;
	    input_tokens: number;
	    output_tokens: number;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new RequestLog(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.timestamp = this.convertValues(source["timestamp"], null);
	        this.provider = source["provider"];
	        this.model = source["model"];
	        this.stream = source["stream"];
	        this.status = source["status"];
	        this.latency_ms = source["latency_ms"];
	        this.input_tokens = source["input_tokens"];
	        this.output_tokens = source["output_tokens"];
	        this.error = source["error"];
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

}

