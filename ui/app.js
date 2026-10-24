const token=document.querySelector('meta[name="csrf-token"]').content;
const byId=id=>document.getElementById(id);
const message=byId('message');
let polling;
function notice(text){message.textContent=text}
async function api(path,body){
  const options=body===undefined?{}:{method:'POST',headers:{'Content-Type':'application/json','X-KeyMoat-CSRF':token},body:JSON.stringify(body)};
  const response=await fetch(path,options);
  const value=await response.json();
  if(!response.ok)throw new Error(value.error||'操作失败');
  return value;
}
async function refresh(){
  const status=await api('/api/status');
  byId('openai-enabled').checked=status.connections.includes('openai');
  byId('github-enabled').checked=status.connections.includes('github');
  byId('openai-key').textContent=status.openai_credential?'钥匙串中已保存凭证':'未找到凭证：先在宿主终端执行 1024 credential openai';
  byId('github-key').textContent=status.github_credential?'钥匙串中已保存凭证':'未找到凭证：先在宿主终端执行 1024 credential github';
  const list=byId('repositories');list.replaceChildren();
  for(const repository of status.repositories){
    const item=document.createElement('li');
    const name=document.createElement('span');name.textContent=repository;
    const revoke=document.createElement('button');revoke.type='button';revoke.textContent='撤销';revoke.title='撤销 '+repository;
    revoke.onclick=()=>act(async()=>{await api('/api/repository',{name:repository,enabled:false});await refresh()});
    item.append(name,revoke);list.append(item);
  }
}
async function act(work){try{notice('');await work()}catch(error){notice(error.message)}}
byId('choose').onclick=()=>act(async()=>{const value=await api('/api/folder',{});byId('folder').value=value.folder});
for(const service of ['openai','github']){
  byId(service+'-enabled').onchange=event=>act(async()=>{
    try{await api('/api/connection',{service,enabled:event.target.checked})}
    finally{await refresh()}
  });
}
byId('grant').onclick=()=>act(async()=>{
  const name=byId('repository').value.trim();
  await api('/api/repository',{name,enabled:true});
  byId('repository').value='';await refresh();
});
byId('run').onclick=()=>act(async()=>{
  await api('/api/run',{folder:byId('folder').value.trim(),model:byId('model').value.trim(),prompt:byId('prompt').value});
  byId('output').textContent='';await poll();
  clearInterval(polling);polling=setInterval(()=>act(poll),1000);
});
byId('cancel').onclick=()=>act(async()=>{await api('/api/cancel',{});await poll()});
async function poll(){
  const status=await api('/api/run');
  byId('run').disabled=status.running;
  byId('cancel').disabled=!status.running;
  byId('task-state').textContent=status.running?'运行中':status.result;
  byId('output').textContent=status.output;
  if(!status.running){clearInterval(polling);polling=undefined}
}
act(async()=>{await refresh();await poll()});
