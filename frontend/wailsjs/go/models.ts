export namespace config {
	
	export class Config {
	    youtube_client_id: string;
	    youtube_client_secret: string;
	    youtube_privacy: string;
	    facebook_page_id: string;
	    facebook_access_token: string;
	    spotify_email: string;
	    spotify_password: string;
	    spotify_show_url: string;
	    output_dir: string;
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.youtube_client_id = source["youtube_client_id"];
	        this.youtube_client_secret = source["youtube_client_secret"];
	        this.youtube_privacy = source["youtube_privacy"];
	        this.facebook_page_id = source["facebook_page_id"];
	        this.facebook_access_token = source["facebook_access_token"];
	        this.spotify_email = source["spotify_email"];
	        this.spotify_password = source["spotify_password"];
	        this.spotify_show_url = source["spotify_show_url"];
	        this.output_dir = source["output_dir"];
	    }
	}

}

export namespace main {
	
	export class SermonMeta {
	    title: string;
	    verse: string;
	    preacher: string;
	
	    static createFrom(source: any = {}) {
	        return new SermonMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.verse = source["verse"];
	        this.preacher = source["preacher"];
	    }
	}
	export class UploadResult {
	    ok: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new UploadResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.message = source["message"];
	    }
	}

}

