import { chromium } from '@playwright/test'
import { createServer } from 'node:http'
import { readFile, writeFile, mkdtemp } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join,extname } from 'node:path'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'
const root=fileURLToPath(new URL('../../',import.meta.url)),dist=join(root,'internal/web/dist')
const artifacts=await mkdtemp(join(tmpdir(),'veyra-ui-'))
const state={state:'ready',detail:'microphone ready',active_turn:0,context:2048,preferences:{reply:'auto',fallback:'en',voices:{}},capabilities:{profile:'composed',interruptions:true,interruption_mode:'cancel',languages:['en','ru'],automatic_language:true,text_input:true,voices:[{id:'en-one',name:'LJSpeech · female',language:'en',default:true},{id:'ru-one',name:'Denis · male',language:'ru',default:true},{id:'ru-two',name:'Russian fixture B',language:'ru',default:false}]}}
state.capabilities.listening='Whisper small · whisper.cpp';state.capabilities.thinking='Qwen3-1.7B · llama.cpp';state.capabilities.speaking='Piper · installed ONNX voices';state.capabilities.reference_conditioning=false;state.reference=''
state.prompts=[{id:"assistant",name:"Everyday assistant",instruction:"Answer directly."},{id:"partner",name:"Thinking partner",instruction:"Challenge assumptions."}];state.preferences.prompt="assistant"
const models={profile:'composed',model:'qwen.gguf',switchable:true,profiles:[{id:'composed',name:'Piper',mode:'composed'},{id:'nano',name:'Chatterbox Nano',mode:'composed'},{id:'native',name:'MiniCPM-o 4.5',mode:'minicpm-native',requires_new:true,experimental:true},{id:'missing',name:'Not installed',unavailable:'Missing assets'}],models:[{id:'qwen.gguf',name:'Qwen'},{id:'chronos.gguf',name:'Chronos'}]};let rejectModels=false,modelDelay=250,modelTimer;
const received=[],failures=[];let active=null,holdNext=false
const wav=Buffer.alloc(44+96000);wav.write('RIFF');wav.writeUInt32LE(wav.length-8,4);wav.write('WAVEfmt ',8);wav.writeUInt32LE(16,16);wav.writeUInt16LE(1,20);wav.writeUInt16LE(1,22);wav.writeUInt32LE(16000,24);wav.writeUInt32LE(32000,28);wav.writeUInt16LE(2,32);wav.writeUInt16LE(16,34);wav.write('data',36);wav.writeUInt32LE(wav.length-44,40)
const server=createServer(async(req,res)=>{try{const url=new URL(req.url,'http://localhost');if(url.pathname==='/api/status'){res.setHeader('Content-Type','application/json');res.end(JSON.stringify(state));return}
if(url.pathname==='/api/models'&&req.method==='GET'){res.setHeader('Content-Type','application/json');res.end(JSON.stringify(models));return}
if(req.method==='POST'){const pieces=[];for await(const p of req)pieces.push(p);const body=Buffer.concat(pieces);received.push({path:url.pathname,body:body.toString()})
 if(url.pathname==='/api/prompt'){const input=JSON.parse(body.toString());state.preferences.prompt=input.id;state.preferences.custom_prompt=input.custom?.trim()??'';res.end('{}');return}
 if(url.pathname==='/api/models/cancel'){clearTimeout(modelTimer);state.state='error';state.detail='Model loading canceled.';res.end('{}');return}
 if(url.pathname==='/api/models'){const input=JSON.parse(body.toString());state.state='loading';state.startup={stage:'Loading speech',started_at:new Date().toISOString(),can_cancel:true};modelTimer=setTimeout(()=>{if(rejectModels){state.state='error';state.detail='Could not load models. Choose a setup and retry.'}else{models.profile=input.profile;models.model=input.model;for(const p of models.profiles)p.requires_new=input.profile==='native'||p.mode==='minicpm-native';state.capabilities.text_input=input.profile!=='native';state.state='ready'}},modelDelay);res.end('{}');return}
 if(url.pathname==='/api/preferences'){state.preferences=JSON.parse(body.toString());res.end('{}');return}
 if(url.pathname==='/api/voice-reference'){if(body.length){assert.equal(body.subarray(0,4).toString(),'RIFF');assert.equal(body.readUInt32LE(24),16000);assert.ok(body.length>=96044)};state.reference=body.length?'Custom reference':'Original reference';res.end('{}');return}
 if(url.pathname==='/api/recover'){if(state.state==='busy'){res.writeHead(409);res.end('still stopping');return};state.state='ready';res.end('{}');return}
 if(url.pathname==='/api/reset'){state.state='ready';res.end('{}');return}
 if(url.pathname==='/api/cancel'){if(active){active.end(JSON.stringify({error:'context canceled'})+'\n');active=null};state.state='busy';setTimeout(()=>{state.state='error';state.detail='canceled'},300);res.end('{}');return}
 if(url.pathname==='/api/text'||url.pathname==='/api/turn'){if(url.pathname==='/api/turn'){assert.equal(body.subarray(0,4).toString(),'RIFF');assert.equal(body.readUInt32LE(24),16000)};state.state='busy';res.setHeader('Content-Type','application/x-ndjson');active=res;res.write(JSON.stringify({phase:'prefill'})+'\n');if(holdNext||body.toString().includes('cancel me')){holdNext=false;return}
 setTimeout(()=>{if(res.writableEnded)return;if(state.capabilities.text_input)res.write(JSON.stringify({transcript:url.pathname==='/api/turn'?'A captured voice message':JSON.parse(body.toString()).text,language:'en'})+'\n');res.write(JSON.stringify({text:'Потоковый ответ ',language:'ru'})+'\n')},120)
 setTimeout(()=>{if(res.writableEnded)return;res.write(JSON.stringify({text:'появляется постепенно.',language:'ru'})+'\n');res.write(JSON.stringify({audio:'/audio/fixture.wav',language:'ru'})+'\n');res.end(JSON.stringify({done:true,first_audio_ms:450,elapsed_ms:650,audio_chunks:1})+'\n');state.state='ready';active=null},500);return}
}
if(url.pathname.startsWith('/audio/')){res.setHeader('Content-Type','audio/wav');res.end(wav);return}
const path=url.pathname==='/'?'index.html':url.pathname.slice(1);if(path.includes('..')){res.writeHead(403);res.end();return};const data=await readFile(join(dist,path));res.setHeader('Content-Type',extname(path)==='.js'?'text/javascript':extname(path)==='.css'?'text/css':extname(path)==='.svg'?'image/svg+xml':'text/html');res.end(data)
}catch(e){failures.push(String(e));res.writeHead(500);res.end(String(e))}})
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const url='http://127.0.0.1:'+server.address().port
const browser=await chromium.launch({executablePath:'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',headless:true,args:['--use-fake-device-for-media-stream','--use-fake-ui-for-media-stream']})
try {
 const context=await browser.newContext({viewport:{width:1440,height:900},permissions:['microphone']})
 const page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(String(e)))
 await page.goto(url);await page.getByRole('heading',{name:'Ready',exact:true}).waitFor()
 await page.screenshot({path:join(artifacts,'device-desktop.png')})
 state.state='loading';state.startup={stage:'Loading Chatterbox Nano',started_at:new Date(Date.now()-21000).toISOString(),can_cancel:true}
 await page.locator('.startup-controls').waitFor()
 await page.screenshot({path:join(artifacts,'startup-controls.png')})
 state.state='ready';await page.locator('.startup-controls').waitFor({state:'hidden'})

 // Real pointer hover must change enabled buttons without repainting disabled actions.
 await page.getByRole('button',{name:'Settings',exact:true}).click()
 for(const mode of ['dark','light']){
  await page.locator('.application').evaluate((el,mode)=>el.setAttribute('data-mode',mode),mode)
  const done=page.getByRole('button',{name:'Done',exact:true})
  await page.mouse.move(0,0);await page.waitForTimeout(200)
  const normal=await done.evaluate(el=>getComputedStyle(el).backgroundColor)
  await done.hover();await page.waitForTimeout(200)
  assert.notEqual(await done.evaluate(el=>getComputedStyle(el).backgroundColor),normal,mode+' secondary button hover')
  await page.getByRole('button',{name:'Models',exact:true}).click()
  const apply=page.getByRole('button',{name:'Apply models',exact:true})
  await page.mouse.move(0,0);await page.waitForTimeout(200)
  const disabled=await apply.evaluate(el=>getComputedStyle(el).backgroundColor)
  await apply.hover();await page.waitForTimeout(200)
  assert.equal(await apply.evaluate(el=>getComputedStyle(el).backgroundColor),disabled,mode+' disabled hover')
  await page.getByLabel('Generation model').selectOption('chronos.gguf')
  await page.mouse.move(0,0);await page.waitForTimeout(200)
  const primary=await apply.evaluate(el=>getComputedStyle(el).backgroundColor)
  await apply.hover();await page.waitForTimeout(200)
  assert.notEqual(await apply.evaluate(el=>getComputedStyle(el).backgroundColor),primary,mode+' primary hover')
  await page.screenshot({path:join(artifacts,'buttons-hover-'+mode+'.png')})
  await page.getByLabel('Generation model').selectOption('qwen.gguf')
  await page.getByRole('button',{name:'Voice',exact:true}).click()
 }
 await page.getByRole('button',{name:'Close settings',exact:true}).click()
 await page.locator('.application').evaluate(el=>el.setAttribute('data-mode','dark'))

 const noise=page.locator('.cloud-orb feTurbulence');const first=await noise.getAttribute('baseFrequency');await page.waitForTimeout(250);assert.notEqual(await noise.getAttribute('baseFrequency'),first)
 await page.emulateMedia({reducedMotion:'reduce'});await page.waitForTimeout(100);const still=await noise.getAttribute('baseFrequency');await page.waitForTimeout(200);assert.equal(await noise.getAttribute('baseFrequency'),still);await page.emulateMedia({reducedMotion:'no-preference'})
 const open=async()=>{await page.getByRole('button',{name:'Settings',exact:true}).click();await page.getByRole('heading',{name:'Settings',exact:true}).waitFor()}
 await open();assert.equal(await page.getByRole('button',{name:'Close settings'}).evaluate(e=>e===document.activeElement),true)
 await page.getByRole('navigation',{name:'Settings sections'}).getByRole('button',{name:'Conversation',exact:true}).click();await page.getByRole('combobox',{name:'System prompt',exact:true}).selectOption('partner');await page.getByRole('textbox',{name:'Custom prompt',exact:true}).fill('Speak warmly, without stage directions.');await Promise.all([page.waitForResponse(r=>r.url().endsWith('/api/prompt')),page.getByRole('button',{name:'Apply prompt',exact:true}).click()])
 assert.equal(state.preferences.prompt,'partner');assert.equal(state.preferences.custom_prompt,'Speak warmly, without stage directions.')
 await page.getByRole('textbox',{name:'Custom prompt',exact:true}).fill('');await Promise.all([page.waitForResponse(r=>r.url().endsWith('/api/prompt')),page.getByRole('button',{name:'Apply prompt',exact:true}).click()]);assert.equal(state.preferences.custom_prompt,'');await page.screenshot({path:join(artifacts,'settings-conversation.png')})
 await page.getByRole('button',{name:'Voice',exact:true}).click();const beforeTest=received.filter(r=>r.path==='/api/turn').length
 await page.getByRole('button',{name:'Test microphone',exact:true}).click();await page.getByRole('heading',{name:'Testing microphone',exact:true,includeHidden:true}).waitFor();await page.waitForTimeout(350);await page.getByRole('button',{name:'Stop microphone test',exact:true}).click();await page.getByLabel('Microphone test playback').waitFor();assert.equal(received.filter(r=>r.path==='/api/turn').length,beforeTest)
 for(const skin of ['lens','scanner','cloud']){await page.getByRole('button',{name:'Appearance',exact:true}).click();await page.locator('select').filter({has:page.locator('option[value="'+skin+'"]')}).selectOption(skin);await page.keyboard.press('Escape');await page.screenshot({path:join(artifacts,skin+'.png')});await open()}
 await page.getByRole('button',{name:'Models',exact:true}).click();await page.getByRole('combobox',{name:'Voice backend',exact:true}).selectOption('nano');await page.getByRole('button',{name:'Apply models',exact:true}).click();await page.getByRole('heading',{name:'Loading model',exact:true,includeHidden:true}).waitFor();await page.getByRole('heading',{name:'Ready',exact:true,includeHidden:true}).waitFor();assert.equal(models.profile,'nano')
 rejectModels=true;await page.getByRole('combobox',{name:'Generation model',exact:true}).selectOption('chronos.gguf');await page.getByRole('button',{name:'Apply models',exact:true}).click();await page.getByRole('heading',{name:'Needs attention',exact:true,includeHidden:true}).waitFor();await page.screenshot({path:join(artifacts,'settings-error.png')});rejectModels=false;await page.getByRole('button',{name:'Retry setup',exact:true}).click();await page.getByRole('heading',{name:'Ready',exact:true,includeHidden:true}).waitFor()
 await page.screenshot({path:join(artifacts,'device-settings.png')});modelDelay=30000;await page.getByRole('combobox',{name:'Generation model',exact:true}).selectOption('qwen.gguf');await page.getByRole('button',{name:'Apply models',exact:true}).click();await page.getByRole('button',{name:'Cancel loading',exact:true}).last().click();await page.getByRole('heading',{name:'Needs attention',exact:true,includeHidden:true}).waitFor();modelDelay=250;await page.getByRole('button',{name:'Retry setup',exact:true}).click();await page.getByRole('heading',{name:'Ready',exact:true,includeHidden:true}).waitFor();await page.keyboard.press('Escape')
 await page.getByRole('button',{name:'Record a message',exact:true}).click();await page.getByRole('button',{name:'Stop & send',exact:true}).waitFor();assert.equal(await page.locator('.visual').getAttribute('data-phase'),'listening');assert.equal(await page.getByRole('button',{name:'Settings',exact:true}).isDisabled(),true);await page.screenshot({path:join(artifacts,'state-listening.png')});await page.waitForTimeout(350);await page.getByRole('button',{name:'Stop & send',exact:true}).click();await page.getByRole('heading',{name:'Speaking',exact:true}).waitFor();assert.equal(await page.locator('.visual').getAttribute('data-phase'),'speaking');await page.screenshot({path:join(artifacts,'state-speaking.png')});await page.getByRole('heading',{name:'Ready',exact:true}).waitFor()
 await page.getByRole('button',{name:/^Conversation/}).click();await page.getByText('появляется постепенно.',{exact:false}).waitFor();await page.screenshot({path:join(artifacts,'conversation.png')});await page.getByRole('textbox',{name:'Message',exact:true}).fill('hello');await page.getByRole('button',{name:'Send',exact:true}).click();await page.getByRole('heading',{name:'Ready',exact:true,includeHidden:true}).waitFor();assert.ok(received.some(r=>r.path==='/api/text'))
 await page.screenshot({path:join(artifacts,'conversation.png')})
 await page.getByRole('button',{name:'Play reply',exact:true}).first().click();await page.getByRole('button',{name:'Stop playback',exact:true}).waitFor();await page.getByRole('button',{name:'Stop playback',exact:true}).click()
;await page.getByRole('button',{name:'New conversation',exact:true}).click();await page.getByRole('heading',{name:'Start a new conversation?',exact:true}).waitFor();await page.getByRole('button',{name:'Cancel',exact:true}).click();assert.ok(await page.locator('.exchange').count());await page.keyboard.press('Escape');await page.getByRole('switch',{name:'Hands-free'}).check();await page.getByRole('heading',{name:'Listening',exact:true}).waitFor();await page.getByRole('switch',{name:'Hands-free'}).uncheck();await page.getByRole('heading',{name:'Ready',exact:true}).waitFor()
 await page.getByRole('button',{name:/^Conversation/}).click();await page.getByRole('textbox',{name:'Message',exact:true}).fill('cancel me');await page.getByRole('button',{name:'Send',exact:true}).click();await page.getByRole('heading',{name:'Thinking',exact:true,includeHidden:true}).waitFor();await page.keyboard.press('Escape');assert.equal(await page.locator('.visual').getAttribute('data-phase'),'thinking');await page.screenshot({path:join(artifacts,'state-thinking.png')});await Promise.all([page.waitForResponse(r=>r.url().endsWith('/api/recover')&&r.status()===200),page.getByRole('button',{name:'Cancel response',exact:true}).click()]);await page.getByRole('heading',{name:'Ready',exact:true}).waitFor();assert.ok(received.some(r=>r.path==='/api/recover'));
 await open();await page.getByRole('button',{name:'Appearance',exact:true}).click();await page.locator('select').filter({has:page.locator('option[value="scanner"]')}).selectOption('scanner');await page.getByRole('combobox',{name:'Theme',exact:true}).selectOption('light');await page.keyboard.press('Escape');await page.screenshot({path:join(artifacts,'device-light.png')});await page.reload();await page.getByRole('heading',{name:'Ready',exact:true}).waitFor();assert.equal(await page.locator('.visual').getAttribute('data-skin'),'scanner')
 await page.setViewportSize({width:390,height:844});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await page.screenshot({path:join(artifacts,'device-mobile.png')});await open();assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await page.keyboard.press('Escape')
 await open();await page.getByRole('button',{name:'Models',exact:true}).click();await page.getByRole('combobox',{name:'Voice backend',exact:true}).selectOption('native');assert.equal(await page.getByRole('combobox',{name:'Generation model',exact:true}).count(),0);assert.equal(await page.getByRole('button',{name:'Start new conversation',exact:true}).isDisabled(),true);await page.screenshot({path:join(artifacts,'settings-minicpm.png')});await page.getByRole('checkbox',{name:'Start a new conversation with this backend.',exact:false}).check();await page.getByRole('button',{name:'Start new conversation',exact:true}).click();await page.getByRole('heading',{name:'Ready',exact:true,includeHidden:true}).waitFor();const last=received.filter(r=>r.path==='/api/models').at(-1);assert.equal(JSON.parse(last.body).new_conversation,true);await page.keyboard.press('Escape');
 assert.deepEqual(errors,[]);assert.deepEqual(failures,[])
 console.log(JSON.stringify({artifacts,checked:['device layout, three persisted skins, reduced motion, both themes, mobile width','custom prompt override and preset restore','model switching, loading, failure and retry','local microphone check, voice capture/playback, typed conversation and hands-free start/stop'],notes:['Fixture API and synthetic microphone. GPU model switching and real voice quality require runtime acceptance.']},null,2))
}finally{await browser.close();server.closeAllConnections();await new Promise(resolve=>server.close(resolve))}
