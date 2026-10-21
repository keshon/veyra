import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
export default defineConfig({plugins:[svelte()],build:{outDir:'../internal/web/dist',emptyOutDir:true},server:{port:5173,proxy:{'/api':'http://127.0.0.1:8090','/audio':'http://127.0.0.1:8090'}}})
