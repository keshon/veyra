import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import {transform} from 'esbuild'
const source=await readFile(new URL('../src/lib/turn-detector.ts',import.meta.url),'utf8')
const {code}=await transform(source,{loader:'ts',format:'esm'})
const {TurnDetector}=await import('data:text/javascript;base64,'+Buffer.from(code).toString('base64'))
const rate=16000,frame=160
const make=(value)=>new Float32Array(frame).fill(value)
function feed(detector,value,seconds,enabled=true){for(let i=0;i<seconds*100;i++)detector.push(make(value),enabled)}
let turns=[];const detector=new TurnDetector(rate,chunks=>turns.push(chunks))
feed(detector,0,2);assert.equal(turns.length,0,'silence must not send')
feed(detector,.1,.1);feed(detector,0,1);assert.equal(turns.length,0,'brief clicks must not send')
feed(detector,.1,1);feed(detector,0,.4);assert.equal(turns.length,0,'a short pause must keep the turn open')
feed(detector,.1,.4);feed(detector,0,.9);assert.equal(turns.length,1,'speech plus end pause should send one turn')
assert.ok(turns[0].reduce((n,c)=>n+c.length,0)<rate*30)
feed(detector,.1,3,false);feed(detector,0,1);assert.equal(turns.length,1,'playback/inference audio must not be retained')
feed(detector,.1,.5);detector.reset();feed(detector,0,1);assert.equal(turns.length,1,'stopping discards the partial turn')
feed(detector,.1,31);assert.equal(turns.length,2,'a continuous utterance must be capped')
assert.ok(turns[1].reduce((n,c)=>n+c.length,0)<=rate*30)
console.log('Turn detection: silence, clicks, intra-speech pause, reply gate, stop and maximum length passed')
