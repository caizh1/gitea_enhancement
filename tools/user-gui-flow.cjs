// 本轮隔离 VS Code 渲染界面操作；Git 命令仅通过可见菜单执行。
const {chromium}=require('/tmp/gitea-gui-acceptance-runtime/node_modules/playwright');
const fs=require('fs');
const out='/Users/archer/Work/gitea-enhancement/docs/evidence/user-permission-20260918';
(async()=>{
 const b=await chromium.connectOverCDP('http://127.0.0.1:9338');const p=b.contexts()[0].pages()[0];const [action,value]=process.argv.slice(2);
 async function cmd(label){if(await p.locator('.quick-input-widget input').isVisible())await p.keyboard.press('Escape');await p.getByRole('button',{name:'Open Quick Access',exact:true}).click();await p.locator('.quick-input-widget input').fill('>'+label);await p.waitForTimeout(250);const rows=p.locator('.quick-input-list .monaco-list-row');let matched=false;for(let i=0;i<await rows.count();i++){let a=await rows.nth(i).getAttribute('aria-label');if(a===label||a.startsWith(label+',')){await rows.nth(i).click();matched=true;break;}}if(!matched)throw Error('未找到完整菜单项：'+label);await p.waitForTimeout(1500);}
 if(action==='command')await cmd(value);
 if(action==='edit'){await p.locator('.editor-instance .native-edit-context').focus();await p.keyboard.press('Meta+a');await p.keyboard.insertText(value);await cmd('File: Save');}
 if(action==='commit'){await cmd('Git: Stage All Changes');await p.locator('.scm-editor .native-edit-context').focus();await p.keyboard.insertText(value);await p.getByText('Commit',{exact:true}).click();}
 if(action==='shot'){await p.screenshot({path:out+'/'+value+'.png'});fs.writeFileSync(out+'/'+value+'.txt','界面可见内容：\n'+await p.locator('body').innerText());}
 console.log((await p.locator('body').innerText()).slice(-4000));await b.close();
})().catch(e=>{console.error(e.message);process.exit(1)});
