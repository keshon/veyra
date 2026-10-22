<script lang="ts">
 import { onMount } from 'svelte'
 let { level=0, animate,phase="ready" }: {level?:number;animate?:boolean;phase?:string}=$props()
 const id='veyra-orb-'+crypto.randomUUID()
 let time=$state(0), energy=$state(0), systemReduced=$state(false)
 const reduced=$derived(animate===undefined?systemReduced:!animate)
 onMount(()=>{
  const preference=matchMedia('(prefers-reduced-motion: reduce)')
  const update=()=>{systemReduced=preference.matches}
  update();preference.addEventListener('change',update)
  let frame=0,last=0
  const animate=(now:number)=>{
   frame=requestAnimationFrame(animate)
   if(document.hidden||now-last<33)return
   const elapsed=Math.min((now-last)/1000,.1);last=now
   energy+= (Math.max(0,Math.min(1,level))-energy)*.24
   if(!reduced)time+=elapsed*(phase==="thinking"?5:phase==="speaking"?2+energy*8:phase==="listening"?1.5+energy*4:.5)
  }
  frame=requestAnimationFrame(animate)
  return()=>{cancelAnimationFrame(frame);preference.removeEventListener('change',update)}
 })
</script>

<svg viewBox="0 0 220 220" aria-hidden="true" focusable="false" class="cloud-orb">
 <defs>
  <clipPath id={id+'-clip'}><circle cx="110" cy="110" r="105"/></clipPath>
  <linearGradient id={id+'-blue'} x1=".2" y1="0" x2=".8" y2="1" gradientUnits="objectBoundingBox"><stop offset="0" stop-color="#343a91"/><stop offset=".42" stop-color="#697dc3"/><stop offset="1" stop-color="#b4dfd9"/></linearGradient>
  <linearGradient id={id+'-wash'} x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="white" stop-opacity="0"/><stop offset=".58" stop-color="white" stop-opacity=".04"/><stop offset="1" stop-color="white" stop-opacity=".7"/></linearGradient>
  <linearGradient id={id+'-mist'} x1="0" y1="0" x2="0" y2="1"><stop stop-color="white" stop-opacity=".02"/><stop offset=".3" stop-color="#dcfff4" stop-opacity=".9"/><stop offset=".65" stop-color="#c9d7fb" stop-opacity=".75"/><stop offset="1" stop-color="white" stop-opacity="0"/></linearGradient>
  <filter id={id+'-cloud'} x="-60" y="-60" width="340" height="340" filterUnits="userSpaceOnUse" color-interpolation-filters="sRGB">
   <feTurbulence type="fractalNoise" baseFrequency={`${.018+Math.sin(time*.3)*.002} .024`} numOctaves="3" seed="8" result="noise"/>
   <feDisplacementMap in="SourceGraphic" in2="noise" scale={reduced?34:36+energy*65} xChannelSelector="R" yChannelSelector="G"/>
   <feGaussianBlur stdDeviation="3.5"/>
  </filter>
  <filter id={id+'-soft'}><feGaussianBlur stdDeviation="13"/></filter>
 </defs>
 <g clip-path={`url(#${id}-clip)`}>
  <circle cx="110" cy="110" r="105" fill={`url(#${id}-blue)`}/>
  <ellipse cx={145+Math.sin(time*.5)*12} cy="77" rx="28" ry="115" fill="#c1b8ff" opacity=".15" filter={`url(#${id}-soft)`} transform="rotate(16 145 77)"/>
  <g transform={`translate(${Math.sin(time*.6)*22} ${Math.sin(time*.8)*12-(reduced?0:energy*12)}) rotate(${22+Math.sin(time*.4)*9} 110 150)`}>
   <ellipse cx="110" cy="153" rx="158" ry={reduced?37:37+energy*12} fill={`url(#${id}-mist)`} filter={`url(#${id}-cloud)`}/>
   <ellipse cx="105" cy="183" rx="150" ry="30" fill="#bcf0e2" opacity=".36" filter={`url(#${id}-soft)`}/>
  </g>
  <circle cx="110" cy="110" r="105" fill={`url(#${id}-wash)`}/>
 </g>
 <circle cx="110" cy="110" r="104.6" fill="none" stroke="#ffffff" stroke-opacity=".24" stroke-width=".7"/>
</svg>

<style>.cloud-orb{display:block;width:100%;height:100%;pointer-events:none}</style>
