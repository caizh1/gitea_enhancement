import json,sys,datetime,subprocess
from pathlib import Path
w=Path('/tmp/gitea-gui-permissions');label=sys.argv[1];expect=sys.argv[2]=='allow';d=w/('clone-'+label)/'gui-permissions';exists=(d/'README.md').exists();logs=sorted((w/'profile/logs').glob('*/window*/exthost/vscode.git/Git.log'));row={'场景':'G06','检查':label+'真实GUI克隆私有仓库','结果':'通过' if exists==expect else '失败','时间':datetime.datetime.now(datetime.timezone.utc).isoformat(),'协议':'HTTP+Token','客户端':'macOS VS Code 1.138.0 CDP真实界面','私有README是否落盘':exists,'目标目录':str(d),'GUI文本':(w/('clone-'+label+'.txt')).read_text(),'日志':logs[-1].read_text()[-6000:]}
if exists: row['克隆HEAD']=subprocess.check_output(['git','rev-parse','HEAD'],cwd=d,text=True).strip();row['克隆文件内容']= (d/'README.md').read_text()
Path('/Users/archer/Work/gitea-enhancement/docs/evidence/user-permission-20260918/gui-permissions-results.jsonl').open('a').write(json.dumps(row,ensure_ascii=False)+'\n');print(row['检查'],row['结果'])
