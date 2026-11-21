// Wspólny skrypt panelu administratora i panelu brygadzisty.
// base: "/admin" albo "/brygadzista".
const N={dostepny:'dostępny',brak_polaczenia:'brak połączenia',wezwany:'wezwany',brak_odpowiedzi:'brak odpowiedzi',idzie:'idzie'};
function esc(s){return String(s==null?'':s).replace(/[&<>"]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]))}
function ago(ms){const s=Math.max(0,Math.round(ms/1000));if(s<60)return s+' s';const m=Math.floor(s/60);if(m<60)return m+' min';const h=Math.floor(m/60);return h+' h '+(m%60)+' min'}
function sec(v){return v?Math.round(v)+' s':'–'}
function battery(t){if(t.battery<0)return '–';return t.battery+'%'+(t.charging?' ⚡':'')}

function renderAlerts(d){
  if(!d.alerts.length)return '';
  let h='<div class="alerts">';
  for(const a of d.alerts){h+='<div class="alert"><b>'+esc(a.station)+'</b>: '+esc(a.worker)+' nie odpowiada od '+ago(d.now-a.at)+'</div>'}
  return h+'</div>';
}

function renderLive(d){
  const tOn=d.tablets.filter(t=>t.online);
  const termOn=d.terminals.filter(t=>t.online);
  let h='<p>Tablety połączone: <b>'+tOn.length+'</b> · terminale połączone: <b>'+termOn.length+'</b> · zalogowani: <b>'+d.workers.length+'</b></p>';
  h+='<table><tr><th>Pracownik</th><th>Status</th><th>Stół</th><th>Od</th></tr>';
  for(const w of d.workers){
    h+='<tr><td>'+esc(w.name)+(w.foreman?' <span class="tag">brygadzista</span>':'')+'</td><td><span class="st '+w.status+'">'+N[w.status]+'</span>'+(w.escalated?' <span class="tag red">alert</span>':'')+'</td><td>'+esc(w.station)+'</td><td>'+(w.since&&w.status!=='dostepny'?ago(d.now-w.since):'')+'</td></tr>'}
  if(!d.workers.length)h+='<tr><td colspan="4">Nikt nie jest zalogowany.</td></tr>';
  h+='</table>';
  return h;
}

function renderTerminals(d){
  let h='<table><tr><th>Pracownik</th><th>Połączony</th><th>Adres IP</th><th>Wersja</th><th>Bateria</th><th>ID terminala</th></tr>';
  for(const t of d.terminals){
    const low=t.battery>=0&&t.battery<=20&&!t.charging;
    h+='<tr><td>'+(t.worker?esc(t.worker):'<i>nikt nie zalogowany</i>')+'</td><td>'+(t.online?'tak':'nie ('+ago(d.now-t.since)+')')+'</td><td>'+esc(t.ip)+'</td><td>'+esc(t.version||'')+'</td><td'+(low?' class="low"':'')+'>'+battery(t)+'</td><td><code>'+esc(t.device)+'</code></td></tr>'}
  if(!d.terminals.length)h+='<tr><td colspan="6">Żaden terminal nie jest połączony.</td></tr>';
  return h+'</table>';
}

function statTable(rows,first){
  let h='<table><tr><th>'+first+'</th><th>Wezwania</th><th>Potwierdzone</th><th>Średni czas</th><th>Mediana</th><th>Najdłużej</th><th>Bez odpowiedzi (wygasłe)</th><th>Alert do brygadzisty</th><th>Anulowane</th></tr>';
  for(const r of rows){h+='<tr><td>'+esc(r.name)+'</td><td>'+r.calls+'</td><td>'+r.acks+'</td><td>'+sec(r.avgSec)+'</td><td>'+sec(r.medianSec)+'</td><td>'+sec(r.maxSec)+'</td><td>'+r.expired+'</td><td>'+r.escalated+'</td><td>'+r.cancelled+'</td></tr>'}
  if(!rows.length)h+='<tr><td colspan="9">Brak wezwań w tym okresie.</td></tr>';
  return h+'</table>';
}

let statPeriod='dzis';
async function loadStats(base){
  const el=document.getElementById('statystyki');
  try{
    const r=await fetch(base+'/statystyki?okres='+statPeriod,{cache:'no-store'});const s=await r.json();
    let h='<p>';
    for(const [k,l] of [['dzis','Dziś'],['7dni','7 dni'],['30dni','30 dni']]){h+='<button class="per'+(k===statPeriod?' on':'')+'" data-p="'+k+'">'+l+'</button> '}
    h+='</p><p>Czas reakcji = od wezwania do „IDĘ” na terminalu.</p>';
    h+='<h3>Pracownicy</h3>'+statTable((s.workers||[]).concat(s.total.calls?[s.total]:[]),'Pracownik');
    h+='<h3>Stoły</h3>'+statTable(s.stations||[],'Stół');
    el.innerHTML=h;
    el.querySelectorAll('button.per').forEach(b=>b.onclick=()=>{statPeriod=b.dataset.p;loadStats(base)});
  }catch(e){el.textContent='Nie udało się pobrać statystyk'}
}
