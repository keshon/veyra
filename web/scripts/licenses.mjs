import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
const entries=[['Svelte','node_modules/svelte/LICENSE.md']]
mkdirSync('public',{recursive:true})
writeFileSync('public/THIRD-PARTY-LICENSES.txt',entries.map(([name,path])=>name+'\n'+'='.repeat(name.length)+'\n'+readFileSync(path,'utf8')).join('\n\n'))
