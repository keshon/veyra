<script lang="ts">
 import type { Snippet } from 'svelte'
 let {open,title,onclose,wide=false,children,footer,navigation}:{open:boolean;title:string;onclose:()=>void;wide?:boolean;children:Snippet;footer?:Snippet;navigation?:Snippet}=$props()
 let element:HTMLDialogElement
 const id='dialog-'+crypto.randomUUID()
 $effect(()=>{if(element){if(open&&!element.open)element.showModal();else if(!open&&element.open)element.close()}})
 function outside(event:PointerEvent){if(event.target!==element)return;const r=element.getBoundingClientRect();if(event.clientX<r.left||event.clientX>r.right||event.clientY<r.top||event.clientY>r.bottom)onclose()}
</script>
<dialog bind:this={element} class:wide class="drawer" aria-labelledby={id} oncancel={event=>{event.preventDefault();onclose()}} onclose={onclose} onpointerdown={outside}>
 <div class="drawer-shell">
  <div class="drawer-head"><h2 {id}>{title}</h2><button type="button" class="icon-button" onclick={onclose} aria-label={'Close '+title.toLowerCase()}><svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18"/></svg></button></div>
  {#if navigation}<nav class="drawer-nav" aria-label={title+' sections'}>{@render navigation()}</nav>{/if}
  <div class="drawer-body">{@render children()}</div>
  {#if footer}<div class="drawer-footer">{@render footer()}</div>{/if}
 </div>
</dialog>
