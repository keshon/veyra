<script lang="ts">
 import { onMount } from 'svelte'
 import VoiceVisual from './lib/VoiceVisual.svelte'
 import * as api from './lib/api'
 import { inspectCapture } from './lib/audio-check'
 import { Recorder, DuplexRecorder,LiveRecorder, Speaker, referenceWAV } from './lib/audio'
 type Turn = {id:number;input:string;reply:string;language:string;phase:string;state:'running'|'complete'|'canceled'|'error';metrics?:api.Event}
 let server= $state<api.Status|null>(null), connected=$state(false), error=$state(''),busy=$state(false),recording=$state(false),starting=$state(false),saving=$state(false),canceling=$state(false),playing=$state(false),seconds=$state(0),draft=$state(''),turns=$state<Turn[]>([]),mode=$state('dark')
 let nextID=0,disposed=false,cancelRequested=false,pollTimer:ReturnType<typeof setTimeout>,recordTimer:ReturnType<typeof setInterval>|undefined,stopTimer:ReturnType<typeof setTimeout>|undefined,controller:AbortController|undefined
 let skin=$state('cloud'),customPrompt=$state(''),promptID=$state(''),promptEditing=$state(false),modelCatalog=$state<api.ModelCatalog|null>(null),selectedProfile=$state(''),selectedModel=$state(''),modelsLoading=$state(false)
 let animationOverride=$state<boolean|null>(null), systemMotion=$state(true)
 const motionEnabled=$derived(animationOverride??systemMotion)
 function setMotion(enabled:boolean){animationOverride=enabled;try{localStorage.setItem('veyra-animation',String(enabled))}catch{}}
 let duplexActive=$state(false),duplexStarting=$state(false),duplexToken='',duplexSequence=0,duplexController:AbortController|undefined,duplexQueue:ArrayBuffer[]=[],duplexSending=false
 let live=$state(false), liveStarting=$state(false), liveSpeaking=$state(false), cooldownUntil=0
 let referenceRecording=$state(false), settingsOpen=$state(false), transcriptOpen=$state(false), micLevel=$state(0), outputLevel=$state(0)
 const recorder=new Recorder(value=>micLevel=value),speaker=new Speaker(value=>{if(playing&&!value)cooldownUntil=Date.now()+450;playing=value},value=>outputLevel=value)
 const liveRecorder=new LiveRecorder(()=>live&&!liveStarting&&!busy&&!playing&&!saving&&!starting&&connected&&server?.state==='ready'&&Date.now()>cooldownUntil,wav=>{if(live&&!busy)void send('turn',wav,'Voice message')},value=>micLevel=value,value=>liveSpeaking=value,()=>{error='Microphone disconnected. Start Live again after reconnecting it.';void stopLive()})
 const duplexRecorder=new DuplexRecorder(wav=>{if(duplexActive){duplexQueue.push(wav);if(duplexQueue.length>3){error="Model cannot keep up with microphone audio.";void stopDuplex()}else void drainDuplex()}},value=>micLevel=value,()=>{error="Microphone disconnected.";void stopDuplex()})

 let microphoneID=$state(''), microphones=$state<MediaDeviceInfo[]>([]),micTesting=$state(false),micSample=$state(''),micSummary=$state('')

 const voiceState=$derived(micTesting?'Testing microphone':duplexStarting?"Starting duplex":duplexActive?playing?"Speaking & listening":"Listening":recording?'Listening':playing?'Speaking':busy?'Thinking':starting?'Starting':saving?'Updating':!connected?'Connecting':server?.state==='error'?'Needs attention':server?.state==='loading'?'Loading model':liveStarting?'Starting Live':live?'Listening':'Ready')

 const level=$derived(micTesting?micLevel:duplexActive?Math.max(micLevel,outputLevel):recording||live&&!busy&&!playing?micLevel:playing?outputLevel:0)

 const available=$derived(!micTesting&&connected&&server?.state==='ready'&&!busy&&!saving&&!starting&&!canceling)

 const locked=$derived(micTesting||duplexActive||duplexStarting||live||liveStarting||busy||recording||starting||saving||canceling||server?.state==='loading'||!connected)

 const languageItems=$derived([...(server?.capabilities.automatic_language?[{value:'auto',label:'Follow my speech'}]:[]),...(server?.capabilities.languages??[]).map(value=>({value,label:value==='en'?'English':value==='ru'?'Russian':value}))])

 const voiceItems=(language:string)=>(server?.capabilities.voices??[]).filter(v=>v.language===language).map(v=>({value:v.id,label:v.name}))

 const voiceValue=(language:string)=>server?.preferences.voices?.[language]??server?.capabilities.voices?.find(v=>v.language===language&&v.default)?.id??null

 function describe(e:unknown){return e instanceof Error?e.message:String(e)}

 async function poll(){try{const current=await api.status();if(disposed)return;if(server?.state==='loading'&&current.state!=='loading'){void api.models().then(value=>modelCatalog=value).catch(()=>{})}server=current;connected=true;if(!promptEditing){customPrompt=current.preferences.custom_prompt??'';promptID=current.preferences.prompt??''}}catch{if(!disposed)connected=false}finally{if(!disposed)pollTimer=setTimeout(poll,1000)}}

 onMount(()=>{const media=matchMedia('(prefers-reduced-motion: reduce)');const motion=()=>systemMotion=!media.matches;motion();media.addEventListener('change',motion);try{const stored=localStorage.getItem('veyra-animation');if(stored!==null)animationOverride=stored==='true';mode=localStorage.getItem('veyra-mode')??'dark'}catch{};try{microphoneID=localStorage.getItem('veyra-microphone')??''}catch{};try{const saved=localStorage.getItem('veyra-skin');if(saved&&['cloud','lens','scanner'].includes(saved))skin=saved}catch{};void refreshMicrophones();void poll();return()=>{media.removeEventListener('change',motion);disposed=true;clearTimeout(micTestTimer);if(micSample)URL.revokeObjectURL(micSample);void micTester.dispose();clearTimeout(pollTimer);clearInterval(recordTimer);clearTimeout(stopTimer);controller?.abort();void duplexRecorder.dispose();duplexController?.abort();void liveRecorder.dispose();void recorder.dispose();void speaker.dispose()}})



 async function choosePrompt(){if(!promptID||locked)return;saving=true;error='';speaker.mute();try{await api.command('prompt',{id:promptID,custom:customPrompt});turns=[];promptEditing=false;server=await api.status()}catch(e){error=describe(e)}finally{saving=false}}
 function chooseSkin(value:string){skin=value;try{localStorage.setItem('veyra-skin',value)}catch{}}
 async function openSettings(){settingsOpen=true;try{modelCatalog=await api.models();selectedProfile=modelCatalog.profile;selectedModel=modelCatalog.model}catch(e){error=describe(e)}}
 async function applyModels(){if(locked||modelsLoading)return;modelsLoading=true;error='';speaker.mute();try{await api.command('models',{profile:selectedProfile,model:selectedModel});server=await api.status();modelCatalog=await api.models()}catch(e){error=describe(e)}finally{modelsLoading=false}}
 async function activate(){if(busy){await cancel();return}if(playing){speaker.mute();return}if(server?.state==='error'){await openSettings();return}if(server?.capabilities.full_duplex){if(duplexActive)await stopDuplex();else await startDuplex();return}if(live){await stopLive();return}await record()}
 async function drainDuplex(){if(duplexSending)return;duplexSending=true;try{while(duplexActive&&duplexQueue.length&&duplexController){const wav=duplexQueue.shift()!;await api.duplexChunk(duplexToken,duplexSequence++,wav,duplexController.signal)}}catch(e){if(duplexActive){error=describe(e);await stopDuplex()}}finally{duplexSending=false}}
 async function startDuplex(){if(!available||duplexActive||duplexStarting)return;duplexStarting=true;error='';duplexController=new AbortController();duplexQueue=[];duplexSequence=0
 try{await speaker.ready();await api.duplex(duplexController.signal,async event=>{
  if(event.token){duplexToken=event.token;await duplexRecorder.start({deviceId:microphoneID});duplexActive=true;duplexStarting=false;turns=[]}
  if(event.text){if(!turns.length)turns=[{id:++nextID,input:'Continuous voice conversation',reply:'',language:'en',phase:'',state:'running'}];turns=turns.map((t,i)=>i===turns.length-1?{...t,reply:t.reply+event.text}:t)}
  if(event.audio&&duplexActive)await speaker.enqueue(event.audio)
 })}catch(e){if(duplexActive||duplexStarting)error=describe(e)}finally{duplexActive=false;duplexStarting=false;await duplexRecorder.dispose();speaker.mute();duplexController=undefined;duplexQueue=[];duplexToken='';try{server=await api.status()}catch{connected=false}}
 }
 async function stopDuplex(){duplexActive=false;duplexStarting=false;await duplexRecorder.dispose();speaker.mute();if(duplexToken)await api.duplexStop(duplexToken).catch(()=>{});duplexController?.abort()}
 $effect(()=>{if(duplexActive&&(!connected||server?.state==='error'))void stopDuplex()})
 async function startLive(){if(!available||recording||live||liveStarting)return;liveStarting=true;error='';try{await speaker.ready();await liveRecorder.start({deviceId:microphoneID});if(disposed){await liveRecorder.dispose();return};live=true}catch(e){error=describe(e);await liveRecorder.dispose()}finally{liveStarting=false}}
 async function stopLive(){live=false;cooldownUntil=Date.now()+400;await liveRecorder.dispose();liveSpeaking=false;speaker.mute()}
 $effect(()=>{if(live&&(!connected||server?.state==='error'))void stopLive()})
 function theme(){mode=mode==='light'?'dark':'light';try{localStorage.setItem('veyra-mode',mode)}catch{}}
 async function preferences(update:Partial<api.Preferences>){if(!server||locked)return;saving=true;error='';try{await api.command('preferences',{...server.preferences,...update});server=await api.status()}catch(e){error=describe(e)}finally{saving=false}}
 async function chooseVoice(language:string,value:string|null){if(value&&server)await preferences({voices:{...server.preferences.voices,[language]:value}})}
 function stopClock(){clearInterval(recordTimer);clearTimeout(stopTimer);recordTimer=undefined;stopTimer=undefined}
 async function record(reference=false){if(recording){await finish();return};if(!available||live||liveStarting)return;starting=true;error='';try{await speaker.ready();await recorder.start({deviceId:microphoneID});if(disposed){await recorder.dispose();return};referenceRecording=reference;recording=true;seconds=0;recordTimer=setInterval(()=>seconds++,1000);stopTimer=setTimeout(()=>void finish(),30000)}catch(e){error=describe(e)}finally{starting=false}}
 async function finish(){if(!recording)return;const reference=referenceRecording;recording=false;referenceRecording=false;stopClock();starting=true;try{const data=await recorder.stop();starting=false;if(!disposed){if(reference){if(data.byteLength<96044)throw Error('Record at least 3 seconds of clean speech.');await setReference(data)}else await send('turn',data,'Voice message')}}catch(e){error=describe(e)}finally{starting=false}}
 async function setReference(data?:ArrayBuffer){saving=true;error='';speaker.mute();try{await api.reference(data);turns=[];server=await api.status()}catch(e){error=describe(e);try{server=await api.status()}catch{connected=false}}finally{saving=false}}
 async function uploadReference(event:Event){const input=event.currentTarget as HTMLInputElement,file=input.files?.[0];input.value='';if(!file||locked)return;saving=true;try{const data=await referenceWAV(file);await setReference(data)}catch(e){error=describe(e)}finally{saving=false}}
 async function send(path:'text'|'turn',input:string|ArrayBuffer,label:string){if(busy||disposed)return;liveSpeaking=false;busy=true;cancelRequested=false;error='';controller=new AbortController();const id=++nextID;turns=[...turns,{id,input:label,reply:'',language:'',phase:path==='turn'?'Listening':'Thinking',state:'running'}]
 const update=(change:Partial<Turn>)=>turns=turns.map(t=>t.id===id?{...t,...change}:t)
 try{await speaker.ready();await api.turn(path,input,controller.signal,async ev=>{if(disposed||controller?.signal.aborted)return;if(ev.transcript)update({input:ev.transcript,language:ev.language??''});if(ev.text){const existing=turns.find(t=>t.id===id)!;update({reply:existing.reply+ev.text,language:ev.language??existing.language})};if(ev.phase)update({phase:ev.phase==='prefill'?'Listening':'Thinking'});if(ev.audio){update({phase:'Speaking'});try{await speaker.enqueue(ev.audio)}catch(e){error='Playback: '+describe(e)}};if(ev.done)update({state:'complete',phase:'',metrics:ev})})}catch(e){if(live)await stopLive();if(cancelRequested||controller.signal.aborted)update({state:'canceled',phase:''});else{update({state:'error',phase:''});error=describe(e)}}finally{cooldownUntil=Date.now()+450;busy=false;controller=undefined;try{server=await api.status()}catch{connected=false}}}
 async function submit(){const text=draft.trim();if(!text||!available||recording||live)return;if(text.length>4000){error='Keep messages under 4,000 characters.';return};draft='';await send('text',text,text)}
 async function cancel(){if(live)await stopLive();cancelRequested=true;canceling=true;const pending=controller;speaker.mute();try{await api.command('cancel')}catch(e){error=describe(e)}finally{pending?.abort();canceling=false}}
 async function reset(){if(locked)return;saving=true;error='';speaker.mute();try{await api.command('reset');turns=[];draft='';server=await api.status()}catch(e){error=describe(e)}finally{saving=false}}

 let micTestTimer:ReturnType<typeof setTimeout>|undefined
 const micTester=new Recorder(value=>micLevel=value)
 async function refreshMicrophones(){try{microphones=(await navigator.mediaDevices.enumerateDevices()).filter(d=>d.kind==='audioinput')}catch(e){error=describe(e)}}
 function chooseMicrophone(id:string|null){if(locked||id===null)return;microphoneID=id==='default'?'':id;try{localStorage.setItem('veyra-microphone',microphoneID)}catch{};micSummary='';if(micSample){URL.revokeObjectURL(micSample);micSample=''}}
 async function testMicrophone(){if(micTesting){await finishMicrophoneTest();return};if(locked)return;micTesting=true;error='';speaker.mute();try{await micTester.start({deviceId:microphoneID});if(disposed){await micTester.dispose();return};await refreshMicrophones();micTestTimer=setTimeout(()=>void finishMicrophoneTest(),8000)}catch(e){micTesting=false;error=describe(e)}}
 async function finishMicrophoneTest(){clearTimeout(micTestTimer);try{const wav=await micTester.stop();if(disposed)return;const info=inspectCapture(wav),settings=micTester.settings;if(micSample)URL.revokeObjectURL(micSample);micSample=URL.createObjectURL(new Blob([wav],{type:'audio/wav'}));micSummary=`${info.seconds.toFixed(1)} s · ${Number.isFinite(info.rmsDb)?info.rmsDb.toFixed(0):'silent'} dBFS · ${info.clippedPercent.toFixed(2)}% clipped · source ${settings?.sampleRate??'unknown'} Hz → 16 kHz mono. ${info.peak<.01?'Input is very quiet; check the device and microphone level.':info.clippedPercent>1?'Input is clipping; lower the microphone gain.':'Play this back: your whole phrase should be clear.'}`}catch(e){error=describe(e)}finally{micTesting=false;micLevel=0}}
 const profile=$derived(server?.capabilities.profile??'Connecting…')
 let conversationDialog: HTMLDialogElement
 $effect(()=>{if(conversationDialog){if(transcriptOpen&&!conversationDialog.open)conversationDialog.showModal();else if(!transcriptOpen&&conversationDialog.open)conversationDialog.close()}})
 let settingsDialog: HTMLDialogElement
 $effect(()=>{if(settingsDialog){if(settingsOpen&&!settingsDialog.open){settingsDialog.showModal();settingsDialog.scrollTop=0}else if(!settingsOpen&&settingsDialog.open)settingsDialog.close()}})
</script>
<svelte:window onkeydown={e=>{if(e.code==='Space'&&e.target===document.body){e.preventDefault();void activate()}}}/>
<div class="application" data-mode={mode}>
 <header><span class="wordmark">Veyra</span><button class="icon-button" onclick={openSettings} aria-label="Settings" title="Settings"><svg viewBox="0 0 24 24"><path d="m9.5 3-.6 2.1-2 .9-2-.5L2.4 10l1.5 1.6v2L2.4 15l2.5 4.4 2-.5 2 .9.6 2.2h5l.6-2.2 2-.9 2 .5 2.5-4.4-1.5-1.4v-2l1.5-1.6L19.1 5.5l-2 .5-2-.9L14.5 3Z"/><circle cx="12" cy="12.5" r="3.2"/></svg></button></header>
 <main class="device">
  <button class="orb voice-orb" data-state={voiceState.toLowerCase()} aria-label={server?.state==='error'?'Open recovery settings':busy?'Cancel response':playing?'Stop speaking':duplexActive?'Stop full duplex':server?.capabilities.full_duplex?'Start full duplex':live?'Stop Live':recording?'Stop & send':'Record a message'} disabled={canceling||duplexStarting||liveStarting||referenceRecording||starting||saving||(!busy&&!playing&&!duplexActive&&!live&&!recording&&!available&&server?.state!=='error')} onclick={activate}><VoiceVisual {skin} {level} animate={motionEnabled} phase={voiceState}/></button>
  <h1 class="state" aria-live="polite">{voiceState}</h1>
  <p class="hint">{server?.state==='error'?'Open Settings to choose a setup and retry.':duplexActive?'Listening while replying. Click to end.':live?busy||playing?'Listening pauses during replies.':liveSpeaking?'Hearing you. Pause to send.':'Speak freely. Click to end.':recording?`Click to send · ${seconds}s / 30s`:busy?'Click to cancel':playing?'Click to stop playback':available?'Click to speak':server?.state==='loading'?'Loading models. Conversation stays in place.':'Connecting to Veyra…'}</p>
  {#if error||server?.state==='error'}<p class="error" role="alert">{error||server?.detail}<button class="quiet" onclick={()=>error=''} aria-label="Dismiss error">Dismiss</button></p>{/if}
 </main>
 <footer class="device-footer"><button class="quiet" onclick={()=>transcriptOpen=true}>Conversation{turns.length?` · ${turns.length}`:''}</button><label class="handsfree">Hands-free<input type="checkbox" role="switch" checked={live||duplexActive} disabled={!live&&!duplexActive&&(!available||recording)} onchange={()=>server?.capabilities.full_duplex?duplexActive?stopDuplex():startDuplex():live?stopLive():startLive()}/></label></footer>
 <dialog bind:this={settingsDialog} onclose={()=>settingsOpen=false} aria-labelledby="settings-title">
  <div class="drawer-head"><h2 id="settings-title">Settings</h2><button class="icon-button" onclick={()=>settingsOpen=false} aria-label="Close settings">×</button></div>
  <section class="group"><p class="group-title">Audio</p>
   <label class="field">Microphone<select value={microphoneID||'default'} onchange={e=>chooseMicrophone(e.currentTarget.value)} disabled={locked}><option value="default">Windows default</option>{#each microphones.filter(d=>d.deviceId!=='default'&&d.deviceId!=='communications') as device,i}<option value={device.deviceId}>{device.label||`Microphone ${i+1}`}</option>{/each}</select></label>
   {#if (server?.capabilities.voices.length??0)<=1}<div class="row"><span>Voice</span><span>{server?.capabilities.voices[0]?.name??'Default voice'}</span></div>{/if}
   {#each server?.capabilities.languages??[] as language}{#if voiceItems(language).length>1}<label class="field">{language==='en'?'English voice':'Russian voice'}<select value={voiceValue(language)??''} onchange={e=>chooseVoice(language,e.currentTarget.value)} disabled={locked}>{#each voiceItems(language) as voice}<option value={voice.value}>{voice.label}</option>{/each}</select></label>{/if}{/each}
   <button class="small-button" disabled={!micTesting&&locked} onclick={testMicrophone}>{micTesting?'Stop microphone test':'Test microphone'}</button>
   {#if micTesting}<p class="field-note" role="status">Speak a phrase · up to 8 seconds. No inference.</p>{/if}
   {#if micSample}<audio controls src={micSample} aria-label="Microphone test playback"></audio><a class="field-note" href={micSample} download="veyra-microphone.wav">Save test recording</a><p class="field-note">{micSummary}</p>{/if}
   {#if (server?.capabilities.languages.length??0)>1}<label class="field">Reply language<select value={server?.preferences.reply??''} onchange={e=>preferences({reply:e.currentTarget.value})} disabled={locked}>{#each languageItems as item}<option value={item.value}>{item.label}</option>{/each}</select></label>{/if}
  </section><div class="divider"></div>
  <section class="group"><p class="group-title">Conversation</p>
   <label class="field">System prompt<select bind:value={promptID} onchange={()=>promptEditing=true} disabled={locked}>{#each server?.prompts??[] as prompt}<option value={prompt.id}>{prompt.name}</option>{/each}</select></label>
   <label class="field">Custom prompt<textarea rows="5" maxlength="4096" placeholder="Optional. Overrides the selected preset." bind:value={customPrompt} oninput={()=>promptEditing=true} disabled={locked}></textarea></label>
   <p class="field-note">Custom text overrides the preset. Clear it to use the preset. Applying starts a new conversation.</p>
   <button class="small-button" onclick={choosePrompt} disabled={locked||!promptEditing}>Apply prompt</button>
  </section><div class="divider"></div>
  <div class="row"><span>Appearance</span><button class="small-button" onclick={theme}>{mode==='dark'?'Dark':'Light'}</button></div>
  <label class="field">Visual<select value={skin} onchange={e=>chooseSkin(e.currentTarget.value)}><option value="cloud">Cloud</option><option value="lens">Lens · HAL-inspired</option><option value="scanner">Scanner · KITT-inspired</option></select></label>
  <label class="check field-note"><input type="checkbox" checked={motionEnabled} onchange={e=>setMotion(e.currentTarget.checked)}/>Animation</label>
  <div class="divider"></div>
  <details><summary>Models</summary><p class="field-note">Recognition: {server?.capabilities.listening??'Loading…'}</p>
   {#if modelCatalog?.switchable}
    <label class="field">Voice backend<select bind:value={selectedProfile} disabled={locked||modelsLoading}>{#each modelCatalog.profiles as item}<option value={item.id} disabled={!!item.unavailable}>{item.name}{item.unavailable?' · not installed':''}</option>{/each}</select></label>
    <label class="field">Generation model<select bind:value={selectedModel} disabled={locked||modelsLoading}>{#each modelCatalog.models as item}<option value={item.id}>{item.name}</option>{/each}</select></label>
    <p class="field-note">Apply between turns. Current models unload first; conversation stays in place. Selection lasts until Veyra closes.</p>
    <button class="small-button" onclick={applyModels} disabled={locked||modelsLoading||(server?.state!=='error'&&selectedProfile===modelCatalog.profile&&selectedModel===modelCatalog.model)}>{modelsLoading||server?.state==='loading'?'Loading…':'Apply models'}</button>
   {:else}<p class="field-note">{profile} · {server?.capabilities.thinking}<br/>Native audio state requires restarting Veyra to change backends.</p>{/if}
  </details>
  {#if server?.capabilities.reference_conditioning}<details><summary>Reference voice</summary><p class="field-note">3–30 seconds of clean speech. Changing it starts a new conversation.</p><button class="small-button" disabled={!referenceRecording&&locked} onclick={()=>record(true)}>{referenceRecording?'Stop & use reference':'Record reference voice'}</button><label class="field">Reference audio<input type="file" accept="audio/*" disabled={locked} onchange={uploadReference}/></label><button class="small-button" disabled={locked} onclick={()=>setReference()}>Restore original voice</button></details>{/if}
 </dialog>
 <dialog bind:this={conversationDialog} onclose={()=>transcriptOpen=false} aria-labelledby="conversation-title">
  <div class="drawer-head"><h2 id="conversation-title">Conversation</h2><button class="icon-button" onclick={()=>transcriptOpen=false} aria-label="Close conversation">×</button></div>
  {#if !turns.length}<p class="empty">Your conversation will appear here.</p>{/if}
  {#each turns as turn (turn.id)}<article class="conversation"><strong>You</strong><p>{turn.input==='Voice message'?'Voice recording · transcript unavailable':turn.input}</p><strong class="reply-name">{server?.setup?.character||'Veyra'}</strong><p>{turn.reply||(turn.state==='running'?'Preparing your reply…':'No complete reply.')}</p>{#if turn.state==='error'||turn.state==='canceled'}<span class="field-note">{turn.state==='error'?'Incomplete':'Canceled'}</span>{/if}</article>{/each}
  {#if server?.capabilities.text_input}<form class="composer" onsubmit={e=>{e.preventDefault();void submit()}}><label class="field">Message<textarea bind:value={draft} disabled={!available||recording||live} onkeydown={e=>{if(e.key==='Enter'&&!e.shiftKey&&!e.isComposing){e.preventDefault();void submit()}}}></textarea></label><button class="small-button" disabled={!available||recording||live||!draft.trim()}>Send</button></form>{/if}
  <div class="new"><button class="small-button" onclick={reset} disabled={locked}>New conversation</button></div>
 </dialog>
</div>
