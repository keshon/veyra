/** Inspect the PCM16 mono WAV produced by the browser capture path. */
export function inspectCapture(wav:ArrayBuffer) {
 const view=new DataView(wav),count=(wav.byteLength-44)/2
 let sum=0,peak=0,clipped=0
 for(let i=0;i<count;i++){const value=view.getInt16(44+i*2,true)/32768;sum+=value*value;peak=Math.max(peak,Math.abs(value));if(Math.abs(value)>=.999)clipped++}
 const rms=count?Math.sqrt(sum/count):0
 return {seconds:count/16000,rmsDb:rms?20*Math.log10(rms):-Infinity,peak,clippedPercent:count?clipped/count*100:0}
}
