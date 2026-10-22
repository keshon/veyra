export type Voice = {id:string;name:string;language:string;default:boolean}
export type Preferences = {prompt?:string;custom_prompt?:string;reply:string;fallback:string;voices?:Record<string,string>}
export type Status = {has_history?:boolean;error_details?:string;startup?:{stage:string;started_at:string;stage_started_at:string;log_dir:string;can_cancel:boolean};setup?:{character?:string};prompts?:{id:string;name:string;instruction:string}[];state:string;detail:string;active_turn:number;context:number;reference?:string;preferences:Preferences;capabilities:{profile:string;languages:string[];automatic_language:boolean;text_input:boolean;voices:Voice[];listening?:string;thinking?:string;speaking?:string;reference_conditioning?:boolean;full_duplex?:boolean}}
export type Event = {kind?:string;turn_id?:number;sequence?:number;text?:string;transcript?:string;language?:string;phase?:string;audio?:string;done?:boolean;error?:string;first_audio_ms?:number;elapsed_ms?:number;audio_chunks?:number}
export async function status():Promise<Status> {const r=await fetch('/api/status',{cache:'no-store',signal:AbortSignal.timeout(3000)});if(!r.ok)throw Error(await r.text());return r.json()}
export async function command(path:string,data?:unknown){const r=await fetch('/api/'+path,{method:'POST',headers:{'Content-Type':'application/json'},body:data===undefined?undefined:JSON.stringify(data)});if(!r.ok)throw Error(await r.text())}
export async function reference(data?:ArrayBuffer){const r=await fetch('/api/voice-reference',{method:'POST',headers:{'Content-Type':'audio/wav'},body:data,signal:AbortSignal.timeout(180000)});if(!r.ok)throw Error(await r.text())}
export async function turn(path:'text'|'turn',input:string|ArrayBuffer,signal:AbortSignal,emit:(event:Event)=>Promise<void>){
 const r=await fetch('/api/'+path,{method:'POST',signal,headers:{'Content-Type':path==='text'?'application/json':'audio/wav'},body:path==='text'?JSON.stringify({text:input}):input as ArrayBuffer})
 if(!r.ok)throw Error(await r.text());if(!r.body)throw Error('No response stream')
 const reader=r.body.getReader(),decoder=new TextDecoder();let pending='',complete=false
 const consume=async(line:string)=>{if(!line.trim())return;const ev:Event=JSON.parse(line);if(ev.error)throw Error(ev.error);await emit(ev);if(ev.done)complete=true}
 try{for(;;){const {value,done}=await reader.read();pending+=decoder.decode(value,{stream:!done});let at;while((at=pending.indexOf('\n'))>=0){await consume(pending.slice(0,at));pending=pending.slice(at+1)}if(done)break;if(pending.length>262144)throw Error('Response event is too large')}if(pending.trim())await consume(pending);if(!complete)throw Error('Response ended without completion')}finally{await reader.cancel().catch(()=>{});reader.releaseLock()}
}

export async function duplex(signal:AbortSignal,emit:(event:Event&{token?:string})=>Promise<void>){
 const r=await fetch('/api/duplex',{method:'POST',signal});if(!r.ok)throw Error(await r.text());if(!r.body)throw Error('No duplex stream')
 const reader=r.body.getReader(),decoder=new TextDecoder();let pending='',done=false
 try{for(;;){const next=await reader.read();pending+=decoder.decode(next.value,{stream:!next.done});let at;while((at=pending.indexOf('\n'))>=0){const line=pending.slice(0,at);pending=pending.slice(at+1);if(!line.trim())continue;const event=JSON.parse(line);if(event.error)throw Error(event.error);await emit(event);if(event.done)done=true};if(pending.length>262144)throw Error('Duplex event exceeds limit');if(next.done)break};if(!done)throw Error('Duplex connection ended unexpectedly')}finally{await reader.cancel().catch(()=>{});reader.releaseLock()}
}
export async function duplexChunk(token:string,sequence:number,wav:ArrayBuffer,signal:AbortSignal){const r=await fetch('/api/duplex/chunk',{method:'POST',signal,headers:{'Content-Type':'audio/wav','X-Duplex-Token':token,'X-Sequence':String(sequence)},body:wav});if(!r.ok)throw Error(await r.text())}
export async function duplexStop(token:string){await fetch('/api/duplex/stop',{method:'POST',headers:{'X-Duplex-Token':token},signal:AbortSignal.timeout(5000)})}

export type ModelCatalog={profile:string;model:string;switchable:boolean;can_restore?:boolean;profiles:{id:string;name:string;unavailable?:string;mode?:string;requires_new?:boolean;experimental?:boolean}[];models:{id:string;name:string}[]}
export async function models():Promise<ModelCatalog>{const r=await fetch("/api/models",{cache:"no-store"});if(!r.ok)throw Error(await r.text());return r.json()}
