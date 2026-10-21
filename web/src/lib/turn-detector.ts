/** Bounded turn detector for quiet-room, half-duplex conversation. */
export class TurnDetector {
 private pre: Float32Array[]=[]
 private chunks: Float32Array[]=[]
 private preSamples=0
 private total=0
 private voiced=0
 private candidate=0
 private quiet=0
 private active=false
 private noise=.004
 constructor(private rate:number, private emit:(chunks:Float32Array[])=>void, private activity:(speaking:boolean)=>void=()=>{}) {}
 reset(){this.pre=[];this.chunks=[];this.preSamples=this.total=this.voiced=this.candidate=this.quiet=0;this.active=false;this.activity(false)}
 push(chunk:Float32Array, enabled:boolean){
  if(!enabled){this.reset();return}
  let sum=0;for(const sample of chunk)sum+=sample*sample
  const rms=Math.sqrt(sum/chunk.length), duration=chunk.length/this.rate
  const speech=rms>Math.max(.018,this.noise*3)
  if(!this.active){
   if(!speech)this.noise=this.noise*.98+Math.min(rms,.02)*.02
   this.pre.push(chunk);this.preSamples+=chunk.length
   while(this.pre.length>1&&this.preSamples-this.pre[0].length>this.rate*.3)this.preSamples-=this.pre.shift()!.length
   this.candidate=speech?this.candidate+duration:0
   if(this.candidate<.16)return
   this.active=true;this.chunks=this.pre;this.total=this.preSamples;this.voiced=this.candidate;this.pre=[];this.preSamples=0;this.activity(true)
   return
  }
  this.chunks.push(chunk);this.total+=chunk.length
  if(speech){this.voiced+=duration;this.quiet=0}else this.quiet+=duration
  if(this.quiet<.8&&this.total<this.rate*29.5)return
  const chunks=this.chunks, valid=this.voiced>=.3
  this.reset()
  if(valid)this.emit(chunks)
 }
}
