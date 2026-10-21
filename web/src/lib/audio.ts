import { TurnDetector } from './turn-detector'
export function pcmWAV(parts:Float32Array[],rate:number):ArrayBuffer {
 const length=parts.reduce((n,p)=>n+p.length,0),pcm=new Float32Array(length);let offset=0;for(const p of parts){pcm.set(p,offset);offset+=p.length}
 const count=Math.floor(length*16000/rate),buffer=new ArrayBuffer(44+count*2),v=new DataView(buffer)
 const str=(at:number,s:string)=>{for(let i=0;i<s.length;i++)v.setUint8(at+i,s.charCodeAt(i))}
 str(0,'RIFF');v.setUint32(4,buffer.byteLength-8,true);str(8,'WAVE');str(12,'fmt ');v.setUint32(16,16,true);v.setUint16(20,1,true);v.setUint16(22,1,true);v.setUint32(24,16000,true);v.setUint32(28,32000,true);v.setUint16(32,2,true);v.setUint16(34,16,true);str(36,'data');v.setUint32(40,count*2,true)
 for(let i=0;i<count;i++){const start=Math.floor(i*rate/16000),end=Math.min(length,Math.max(start+1,Math.floor((i+1)*rate/16000)));let sample=0;for(let j=start;j<end;j++)sample+=pcm[j];sample=Math.max(-1,Math.min(1,sample/(end-start)));v.setInt16(44+i*2,sample*(sample<0?32768:32767),true)}return buffer
}
function rms(samples:Float32Array){let sum=0;for(const sample of samples)sum+=sample*sample;return Math.sqrt(sum/samples.length)}
export type CaptureOptions = {deviceId?:string}

class Microphone {
 private stream?:MediaStream;private context?:AudioContext;private worklet?:AudioWorkletNode
 settings?:MediaTrackSettings
 async start(capture:(chunk:Float32Array,rate:number)=>void, ended:()=>void=()=>{},options:CaptureOptions={}){
  try{
   this.stream=await navigator.mediaDevices.getUserMedia({audio:{channelCount:1,echoCancellation:true,noiseSuppression:true,autoGainControl:true,...(options.deviceId?{deviceId:{exact:options.deviceId}}:{})}})
   this.settings=this.stream.getAudioTracks()[0].getSettings()
   this.stream.getAudioTracks().forEach(track=>track.addEventListener('ended',ended,{once:true}))
   this.context=new AudioContext({sampleRate:16000});await this.context.resume()
   const source=`class Capture extends AudioWorkletProcessor{process(inputs){if(inputs[0]?.[0])this.port.postMessage(inputs[0][0].slice());return true}}registerProcessor('capture',Capture)`
   const url=URL.createObjectURL(new Blob([source],{type:'application/javascript'}))
   try{await this.context.audioWorklet.addModule(url)}finally{URL.revokeObjectURL(url)}
   this.worklet=new AudioWorkletNode(this.context,'capture')
   const rate=this.context.sampleRate
   this.worklet.port.onmessage=e=>capture(e.data,rate)
   const silence=this.context.createGain();silence.gain.value=0
   this.context.createMediaStreamSource(this.stream).connect(this.worklet);this.worklet.connect(silence).connect(this.context.destination)
  }catch(e){await this.dispose();throw e}
 }
 async dispose(){this.stream?.getTracks().forEach(t=>t.stop());this.stream=undefined;if(this.worklet){this.worklet.port.onmessage=null;this.worklet.disconnect()};this.worklet=undefined;const context=this.context;this.context=undefined;if(context&&context.state!=='closed')await context.close()}
}

export class Recorder {
 private microphone=new Microphone();private parts:Float32Array[]=[];private rate=16000
 constructor(private level:(value:number)=>void=()=>{}){}
 async start(options:CaptureOptions={}){this.parts=[];await this.microphone.start((chunk,rate)=>{this.rate=rate;this.parts.push(chunk);this.level(Math.min(1,rms(chunk)*6))},()=>{},options)}
 get settings(){return this.microphone.settings}
 async stop(){await this.dispose();return pcmWAV(this.parts,this.rate)}
 async dispose(){await this.microphone.dispose();this.level(0)}
}

export class LiveRecorder {
 private microphone=new Microphone();private detector?:TurnDetector
 constructor(private enabled:()=>boolean,private turn:(wav:ArrayBuffer)=>void,private level:(value:number)=>void,private activity:(speaking:boolean)=>void,private ended:()=>void){}
 async start(options:CaptureOptions={}){await this.microphone.start((chunk,rate)=>{
  if(!this.detector)this.detector=new TurnDetector(rate,parts=>this.turn(pcmWAV(parts,rate)),this.activity)
  const enabled=this.enabled();this.level(enabled?Math.min(1,rms(chunk)*6):0);this.detector.push(chunk,enabled)
 },this.ended,options)}
 async dispose(){await this.microphone.dispose();this.detector?.reset();this.detector=undefined;this.level(0)}
}

// Continuous capture includes silence and remains active throughout playback.
export class DuplexRecorder {
 private microphone=new Microphone();private parts:Float32Array[]=[];private length=0
 constructor(private chunk:(wav:ArrayBuffer)=>void,private level:(value:number)=>void,private ended:()=>void){}
 async start(options:CaptureOptions={}){this.parts=[];this.length=0;await this.microphone.start((samples,rate)=>{
  this.level(Math.min(1,rms(samples)*6));let offset=0
  while(offset<samples.length){const count=Math.min(rate-this.length,samples.length-offset);this.parts.push(samples.slice(offset,offset+count));this.length+=count;offset+=count;if(this.length===rate){this.chunk(pcmWAV(this.parts,rate));this.parts=[];this.length=0}}
 },this.ended,options)}
 async dispose(){await this.microphone.dispose();this.parts=[];this.length=0;this.level(0)}
}

export async function referenceWAV(file:File):Promise<ArrayBuffer> {
 if(file.size>10000000)throw Error('Use a reference file smaller than 10 MB.')
 const context=new AudioContext()
 try{const audio=await context.decodeAudioData(await file.arrayBuffer());if(audio.duration<3||audio.duration>30)throw Error('Use 3–30 seconds of clean speech.');const mono=new Float32Array(audio.length);for(let channel=0;channel<audio.numberOfChannels;channel++){const data=audio.getChannelData(channel);for(let i=0;i<mono.length;i++)mono[i]+=data[i]/audio.numberOfChannels};return pcmWAV([mono],audio.sampleRate)}finally{await context.close()}
}
export class Speaker {
 private context?:AudioContext;private sources=new Set<AudioBufferSourceNode>();private next=0;private epoch=0;private muted=false
 private analyser?:AnalyserNode;private frame=0
 constructor(private changed:(playing:boolean)=>void,private level:(value:number)=>void=()=>{}){}
 private measure(){if(this.frame||!this.analyser)return;const data=new Float32Array(this.analyser.fftSize);const update=()=>{if(!this.sources.size){this.frame=0;this.level(0);return};this.analyser!.getFloatTimeDomainData(data);let sum=0;for(const sample of data)sum+=sample*sample;this.level(Math.min(1,Math.sqrt(sum/data.length)*6));this.frame=requestAnimationFrame(update)};this.frame=requestAnimationFrame(update)}
 async ready(){this.mute();this.muted=false;if(!this.context){this.context=new AudioContext();this.analyser=this.context.createAnalyser();this.analyser.fftSize=256;this.analyser.connect(this.context.destination)};await this.context.resume()}
 async enqueue(url:string){if(this.muted)return;const epoch=this.epoch;const r=await fetch(url);if(!r.ok)throw Error('Speech file could not be loaded');const data=await r.arrayBuffer();const context=this.context;if(!context)return;const buffer=await context.decodeAudioData(data);if(this.muted||epoch!==this.epoch)return
 const source=context.createBufferSource();source.buffer=buffer;source.connect(this.analyser!);const at=Math.max(context.currentTime+.04,this.next);this.next=at+buffer.duration;this.sources.add(source);source.onended=()=>{this.sources.delete(source);this.changed(this.sources.size>0)};source.start(at);this.changed(true);this.measure()}
 mute(){cancelAnimationFrame(this.frame);this.frame=0;this.level(0);this.muted=true;this.epoch++;for(const source of this.sources){source.onended=null;try{source.stop()}catch{}}this.sources.clear();this.next=0;this.changed(false)}
 async dispose(){this.mute();await this.context?.close();this.context=undefined}
}
