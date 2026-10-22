import { test, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, writeFileSync, readFileSync, mkdirSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { execFileSync, spawnSync } from 'node:child_process'
const root = fileURLToPath(new URL('../', import.meta.url))
const nodeProduct = readFileSync(join(root, 'go.mod'), 'utf8').includes('github.com/KazuhaHub/passwall-node/v4')
const scratch = mkdtempSync(join(tmpdir(), 'release-stages-'))
const tool = join(scratch, 'release-stage')
const scripts = join(root, '.github/scripts')
before(() => execFileSync('go', ['build', '-o', tool, nodeProduct ? './deployment/cmd/release-stage' : './cmd/release-stage'], { cwd: root, env: {...process.env, GOWORK:'off'} }))
after(() => rmSync(scratch, { recursive:true, force:true }))
function fixture() {
 const dir = mkdtempSync(join(scratch, 'case-')); const bin = join(dir,'bin');mkdirSync(bin)
 return {dir,bin,log:join(dir,'calls'),env:{...process.env,PATH:`${bin}:${process.env.PATH}`,STAGE_TOOL:tool,RUNNER_TEMP:dir}}
}
function shim(f, name, body) {writeFileSync(join(f.bin,name), '#!/usr/bin/env node\n'+body,{mode:0o755})}
function calls(f) { try {return readFileSync(f.log,'utf8')} catch {return ''} }
function pointers(version, approved, versions={}, error='') {
 const f=fixture(); f.env={...f.env,IMAGE:'ghcr.io/example/product',VERSION:version,APPROVED:String(approved),DIGEST:'sha256:'+'a'.repeat(64),CURRENT:JSON.stringify(versions),CALLS:f.log,ERROR:error}
 shim(f,'docker',`const a=process.argv.slice(2), fs=require('node:fs');
 if(a.includes('create')) {fs.appendFileSync(process.env.CALLS, a.join(' ')+'\\n');process.exit(0)}
 const alias=a[3].split(':').at(-1); const current=JSON.parse(process.env.CURRENT)[alias];
 if(process.env.ERROR){process.stderr.write(process.env.ERROR);process.exit(1)}
 if(a.at(-1).includes('Manifest')){console.log(JSON.stringify({digest:process.env.DIGEST}));process.exit(0)}
 if(!current){process.stderr.write('manifest unknown');process.exit(1)}
 console.log(JSON.stringify({Labels:{'org.opencontainers.image.version':current}}));`)
 const result=spawnSync('bash',[join(scripts,'update-image-channels.sh')],{env:f.env,encoding:'utf8'}); return {...f,result}
}
test('Beta and RC never move stable/latest; a staged plain build moves no aliases',()=>{
 for(const [v,aliases] of [['4.1.0-beta.2',['beta','testing']],['4.1.0-rc.1',['rc','beta','testing']],['4.1.0',[]]]) {
  const f=pointers(v,false);assert.equal(f.result.status,0,f.result.stderr)
  const actual=calls(f).trim().split('\n').filter(Boolean).map(l=>l.match(/-t ghcr.io\/example\/product:([^ ]+)/)[1]);assert.deepEqual(actual,aliases)
 }
})
test('approved final reaches stable/latest and RC while preserving a newer beta',()=>{
 const f=pointers('4.1.0',true,{stable:'4.0.2',latest:'4.0.2',rc:'4.1.0-rc.2',beta:'4.2.0-beta.1',testing:'4.2.0-beta.1'})
 assert.equal(f.result.status,0,f.result.stderr);assert.match(calls(f),/:stable /);assert.match(calls(f),/:latest /);assert.match(calls(f),/:rc /);assert.doesNotMatch(calls(f),/:beta |:testing /)
})
test('stable rollback and registry failures refuse before any pointer moves',()=>{
 for(const f of [pointers('4.1.0',true,{stable:'4.2.0',latest:'4.2.0'}),pointers('4.1.0-beta.2',false,{},'network timeout')]) {
  assert.notEqual(f.result.status,0);assert.equal(calls(f),'')
 }
})
function sourceFixture(source='v4.1.0-beta.3') {
 const f=fixture(); const repo=join(f.dir,'repo'),remote=join(f.dir,'remote.git');mkdirSync(repo)
 const git=(...a)=>execFileSync('git',a,{cwd:repo,encoding:'utf8',stdio:['ignore','pipe','pipe']}).trim()
 git('init','-b','main');git('config','user.name','Release Operator');git('config','user.email','release@example.test')
 writeFileSync(join(repo,'source'),'tested');git('add','.');git('commit','-m','Tested candidate');const sha=git('rev-parse','HEAD');git('tag',source)
 writeFileSync(join(repo,'source'),'newer main');git('add','.');git('commit','-m','Later main change')
 execFileSync('git',['init','--bare',remote],{stdio:'ignore'});git('remote','add','origin',remote);git('push','origin','main','--tags')
 f.env={...f.env,GITHUB_REF:'refs/heads/main',GITHUB_REPOSITORY:'example/product',GITHUB_ACTOR:'release-operator',GITHUB_STEP_SUMMARY:join(f.dir,'summary'),SOURCE_TAG:source,TARGET_STAGE:'stable',CALLS:f.log,SOURCE_SHA:sha,MOCK_STATE:f.dir,REQUIRE_NODE_ACCEPTANCE:String(nodeProduct)}
 shim(f,'gh',`const a=process.argv.slice(2), fs=require('node:fs'), path=require('node:path');
 fs.appendFileSync(process.env.CALLS,JSON.stringify(a)+'\\n');
 const opt=k=>a[a.indexOf(k)+1];const state=path.join(process.env.MOCK_STATE,'dispatched');
 if(a[0]==='api'){
  if(a[1].includes('/releases/tags/')) console.log(JSON.stringify({tag_name:process.env.SOURCE_TAG,draft:false}));
  else if(a[1].includes('/commits/main')) console.log(process.env.SOURCE_SHA);
  else console.log('true');process.exit(0)
 }
 if(a[0]==='workflow' && a[1]==='run'){fs.appendFileSync(state,a[2]+'\\n');process.exit(0)}
 if(a[0]==='run' && a[1]==='view') {console.log(opt('--jq')==='.status'?'completed':process.env.PUBLICATION_FAILURE?'failure':'success');process.exit(0)}
 if(a[0]==='run' && a[1]==='list') {
  const w=opt('--workflow');
  if(w==='test.yml') console.log('success');
  else if(w.includes('acceptance')) console.log('222');
  else if(!a.includes('--branch')) console.log('1');
  else if(fs.existsSync(state))console.log('111');
  process.exit(0)
 }
 process.stderr.write('unexpected gh invocation');process.exit(1);`)
 return {...f,repo,git,sha}
}
function advance(f) {return spawnSync('bash',[join(scripts,'advance-release.sh')],{cwd:f.repo,env:f.env,encoding:'utf8'})}
test('Beta to RC uses the selected commit, resumes the same tag and publishes only once',()=>{
 const f=sourceFixture();f.env.TARGET_STAGE='rc';let r=advance(f);assert.equal(r.status,0,r.stderr)
 assert.equal(f.git('rev-parse','v4.1.0-rc.1^{commit}'),f.sha);assert.notEqual(f.git('rev-parse','HEAD'),f.sha)
 r=advance(f);assert.equal(r.status,0,r.stderr);assert.equal(f.git('tag','--list','v4.1.0-rc.*'),'v4.1.0-rc.1')
 assert.equal(calls(f).split('\n').filter(l=>l.includes('"workflow","run","release.yml"')).length,1)
 assert.doesNotMatch(calls(f),/"workflow","run","promote.yml"/)
})
test('Beta and RC can become final only after publication/acceptance, via verified promotion',()=>{
 for(const source of ['v4.1.0-beta.3','v4.1.0-rc.2']) {
  const f=sourceFixture(source);const r=advance(f);assert.equal(r.status,0,r.stderr)
  assert.equal(f.git('rev-parse','v4.1.0^{commit}'),f.sha)
  const log=calls(f);assert(log.indexOf('"workflow","run","release.yml"')<log.indexOf('"workflow","run","promote.yml"'))
  if(nodeProduct) assert(log.indexOf('installation-acceptance.yml')<log.indexOf('"workflow","run","promote.yml"'))
 }
})
test('a conflicting target or failed publication cannot be promoted',()=>{
 const f=sourceFixture();f.git('tag','v4.1.0');f.git('push','origin','v4.1.0');let r=advance(f)
 assert.notEqual(r.status,0);assert.match(r.stderr,/another commit/);assert.doesNotMatch(calls(f),/"workflow","run"/)
 const failure=sourceFixture();failure.env.PUBLICATION_FAILURE='1';r=advance(failure)
 assert.notEqual(r.status,0);assert.match(r.stderr,/failed/);assert.doesNotMatch(calls(failure),/"workflow","run","promote.yml"/)
 delete failure.env.PUBLICATION_FAILURE; r=advance(failure);assert.equal(r.status,0,r.stderr)
 assert.equal(calls(failure).split('\n').filter(l=>l.includes('"workflow","run","release.yml"')).length,1)
})
