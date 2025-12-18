export namespace config {
	
	export class ConfigLoader {
	
	
	    static createFrom(source: any = {}) {
	        return new ConfigLoader(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	
	    }
	}

}

export namespace main {
	
	export class CLIConfigDirs {
	    claudeConfigDir: string;
	    codexConfigDir: string;
	
	    static createFrom(source: any = {}) {
	        return new CLIConfigDirs(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.claudeConfigDir = source["claudeConfigDir"];
	        this.codexConfigDir = source["codexConfigDir"];
	    }
	}
	export class CLIConfigFile {
	    name: string;
	    content: string;
	    exists: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CLIConfigFile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.content = source["content"];
	        this.exists = source["exists"];
	    }
	}
	export class CLIConfigResult {
	    success: boolean;
	    message?: string;
	    files?: CLIConfigFile[];
	
	    static createFrom(source: any = {}) {
	        return new CLIConfigResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.message = source["message"];
	        this.files = this.convertValues(source["files"], CLIConfigFile);
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
	export class CLIVersionInfo {
	    nodeVersion: string;
	    nodeInstalled: boolean;
	    cliVersion: string;
	    cliInstalled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CLIVersionInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.nodeVersion = source["nodeVersion"];
	        this.nodeInstalled = source["nodeInstalled"];
	        this.cliVersion = source["cliVersion"];
	        this.cliInstalled = source["cliInstalled"];
	    }
	}
	export class EndpointInfo {
	    id: number;
	    name: string;
	    apiUrl: string;
	    apiKey?: string;
	    active: boolean;
	    enabled: boolean;
	    interfaceType: string;
	    vendorId: number;
	    vendorName?: string;
	    model?: string;
	    remark?: string;
	    priority: number;
	    todayRequests: number;
	    todayErrors: number;
	    todayInput: number;
	    todayOutput: number;
	
	    static createFrom(source: any = {}) {
	        return new EndpointInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.apiUrl = source["apiUrl"];
	        this.apiKey = source["apiKey"];
	        this.active = source["active"];
	        this.enabled = source["enabled"];
	        this.interfaceType = source["interfaceType"];
	        this.vendorId = source["vendorId"];
	        this.vendorName = source["vendorName"];
	        this.model = source["model"];
	        this.remark = source["remark"];
	        this.priority = source["priority"];
	        this.todayRequests = source["todayRequests"];
	        this.todayErrors = source["todayErrors"];
	        this.todayInput = source["todayInput"];
	        this.todayOutput = source["todayOutput"];
	    }
	}
	export class EndpointInput {
	    id: number;
	    name: string;
	    apiUrl: string;
	    apiKey: string;
	    active: boolean;
	    enabled: boolean;
	    interfaceType: string;
	    vendorId: number;
	    model?: string;
	    remark?: string;
	    priority: number;
	
	    static createFrom(source: any = {}) {
	        return new EndpointInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.apiUrl = source["apiUrl"];
	        this.apiKey = source["apiKey"];
	        this.active = source["active"];
	        this.enabled = source["enabled"];
	        this.interfaceType = source["interfaceType"];
	        this.vendorId = source["vendorId"];
	        this.model = source["model"];
	        this.remark = source["remark"];
	        this.priority = source["priority"];
	    }
	}
	export class EndpointStatsSummaryInfo {
	    endpointId: string;
	    endpointName: string;
	    vendorName: string;
	    date?: string;
	    inputTokens: number;
	    outputTokens: number;
	    cachedCreate: number;
	    cachedRead: number;
	    reasoning: number;
	    total: number;
	    requestCount: number;
	
	    static createFrom(source: any = {}) {
	        return new EndpointStatsSummaryInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.endpointId = source["endpointId"];
	        this.endpointName = source["endpointName"];
	        this.vendorName = source["vendorName"];
	        this.date = source["date"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.cachedCreate = source["cachedCreate"];
	        this.cachedRead = source["cachedRead"];
	        this.reasoning = source["reasoning"];
	        this.total = source["total"];
	        this.requestCount = source["requestCount"];
	    }
	}
	export class InstallCLIResult {
	    success: boolean;
	    message: string;
	    output: string;
	
	    static createFrom(source: any = {}) {
	        return new InstallCLIResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.message = source["message"];
	        this.output = source["output"];
	    }
	}
	export class InterfaceTypeStatsSummaryInfo {
	    interfaceType: string;
	    inputTokens: number;
	    outputTokens: number;
	    cachedCreate: number;
	    cachedRead: number;
	    reasoning: number;
	    total: number;
	    requestCount: number;
	    endpoints: EndpointStatsSummaryInfo[];
	
	    static createFrom(source: any = {}) {
	        return new InterfaceTypeStatsSummaryInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.interfaceType = source["interfaceType"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.cachedCreate = source["cachedCreate"];
	        this.cachedRead = source["cachedRead"];
	        this.reasoning = source["reasoning"];
	        this.total = source["total"];
	        this.requestCount = source["requestCount"];
	        this.endpoints = this.convertValues(source["endpoints"], EndpointStatsSummaryInfo);
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
	export class ProcessCodexConfigResult {
	    configToml: string;
	    authJson: string;
	
	    static createFrom(source: any = {}) {
	        return new ProcessCodexConfigResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.configToml = source["configToml"];
	        this.authJson = source["authJson"];
	    }
	}
	export class RequestLogDetailInfo {
	    id: string;
	    interfaceType: string;
	    vendorName: string;
	    endpointName: string;
	    path: string;
	    runTime: number;
	    status: string;
	    timestamp: string;
	    method: string;
	    statusCode: number;
	    targetUrl: string;
	    upstreamAuth: string;
	    requestHeaders: Record<string, string>;
	    responseStream: string;
	
	    static createFrom(source: any = {}) {
	        return new RequestLogDetailInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.interfaceType = source["interfaceType"];
	        this.vendorName = source["vendorName"];
	        this.endpointName = source["endpointName"];
	        this.path = source["path"];
	        this.runTime = source["runTime"];
	        this.status = source["status"];
	        this.timestamp = source["timestamp"];
	        this.method = source["method"];
	        this.statusCode = source["statusCode"];
	        this.targetUrl = source["targetUrl"];
	        this.upstreamAuth = source["upstreamAuth"];
	        this.requestHeaders = source["requestHeaders"];
	        this.responseStream = source["responseStream"];
	    }
	}
	export class RequestLogInfo {
	    id: string;
	    interfaceType: string;
	    vendorName: string;
	    endpointName: string;
	    path: string;
	    runTime: number;
	    status: string;
	    timestamp: string;
	
	    static createFrom(source: any = {}) {
	        return new RequestLogInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.interfaceType = source["interfaceType"];
	        this.vendorName = source["vendorName"];
	        this.endpointName = source["endpointName"];
	        this.path = source["path"];
	        this.runTime = source["runTime"];
	        this.status = source["status"];
	        this.timestamp = source["timestamp"];
	    }
	}
	export class Settings {
	    port: number;
	    apiKey: string;
	    fallback: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.port = source["port"];
	        this.apiKey = source["apiKey"];
	        this.fallback = source["fallback"];
	    }
	}
	export class TestEndpointParams {
	    apiUrl: string;
	    apiKey: string;
	    interfaceType: string;
	    model: string;
	    reasoning?: string;
	
	    static createFrom(source: any = {}) {
	        return new TestEndpointParams(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.apiUrl = source["apiUrl"];
	        this.apiKey = source["apiKey"];
	        this.interfaceType = source["interfaceType"];
	        this.model = source["model"];
	        this.reasoning = source["reasoning"];
	    }
	}
	export class TokenStatsInfo {
	    endpointName: string;
	    vendorName: string;
	    inputTokens: number;
	    cachedCreate: number;
	    cachedRead: number;
	    outputTokens: number;
	    reasoning: number;
	    total: number;
	
	    static createFrom(source: any = {}) {
	        return new TokenStatsInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.endpointName = source["endpointName"];
	        this.vendorName = source["vendorName"];
	        this.inputTokens = source["inputTokens"];
	        this.cachedCreate = source["cachedCreate"];
	        this.cachedRead = source["cachedRead"];
	        this.outputTokens = source["outputTokens"];
	        this.reasoning = source["reasoning"];
	        this.total = source["total"];
	    }
	}
	export class VendorInfo {
	    id: number;
	    name: string;
	    homeUrl: string;
	    apiUrl: string;
	    remark?: string;
	
	    static createFrom(source: any = {}) {
	        return new VendorInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.homeUrl = source["homeUrl"];
	        this.apiUrl = source["apiUrl"];
	        this.remark = source["remark"];
	    }
	}
	export class VendorStatsSummaryInfo {
	    vendorId: string;
	    vendorName: string;
	    inputTokens: number;
	    outputTokens: number;
	    cachedCreate: number;
	    cachedRead: number;
	    reasoning: number;
	    total: number;
	    endpoints: EndpointStatsSummaryInfo[];
	
	    static createFrom(source: any = {}) {
	        return new VendorStatsSummaryInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.vendorId = source["vendorId"];
	        this.vendorName = source["vendorName"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.cachedCreate = source["cachedCreate"];
	        this.cachedRead = source["cachedRead"];
	        this.reasoning = source["reasoning"];
	        this.total = source["total"];
	        this.endpoints = this.convertValues(source["endpoints"], EndpointStatsSummaryInfo);
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

export namespace proxy {
	
	export class DefaultRouter {
	
	
	    static createFrom(source: any = {}) {
	        return new DefaultRouter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	
	    }
	}
	export class ProxyServer {
	
	
	    static createFrom(source: any = {}) {
	        return new ProxyServer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	
	    }
	}
	export class WSHub {
	
	
	    static createFrom(source: any = {}) {
	        return new WSHub(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	
	    }
	}

}

export namespace statsdb {
	
	export class SQLiteVendorStatsStore {
	
	
	    static createFrom(source: any = {}) {
	        return new SQLiteVendorStatsStore(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	
	    }
	}

}

