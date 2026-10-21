import assert from 'node:assert/strict'
import {build} from 'esbuild'
const load=async entry=>{
 const result=await build({entryPoints:[entry],bundle:true,write:false,format:'esm',platform:'browser'})
 return import('data:text/javascript;base64,'+Buffer.from(result.outputFiles[0].text).toString('base64'))
}
const {pcmWAV}=await load('src/lib/audio.ts')
const {inspectCapture}=await load('src/lib/audio-check.ts')
const rate=48000
const speech=Float32Array.from({length:rate},(_,i)=>.2*Math.sin(2*Math.PI*220*i/rate))
const wav=pcmWAV([speech],rate),view=new DataView(wav)
assert.equal(view.getUint32(24,true),16000)
assert.equal(view.getUint16(22,true),1)
assert.equal(wav.byteLength,32044)
const info=inspectCapture(wav)
assert.equal(info.seconds,1)
assert.ok(info.peak>.19&&info.peak<.21)
assert.ok(info.rmsDb>-18&&info.rmsDb<-16)
assert.equal(info.clippedPercent,0)
assert.equal(inspectCapture(pcmWAV([new Float32Array(16000)],16000)).rmsDb,-Infinity)
assert.equal(inspectCapture(pcmWAV([new Float32Array(16000).fill(1.5)],16000)).clippedPercent,100)
console.log('Capture integrity: 48 kHz resampling, duration, level, silence and clipping passed')
