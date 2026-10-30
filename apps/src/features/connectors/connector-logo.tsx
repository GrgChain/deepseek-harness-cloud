import {Plug} from 'lucide-react';

const connectorLogos:Record<string,string>={
 'tencent-docs':'tencent-docs.png',
 'tencent-ima':'tencent-ima.svg',
 'tencent-meeting':'tencent-meeting.png',
 'tencent-news':'tencent-news.png',
 'wechat-reading':'wechat-reading.png',
 'tencent-lexiang':'tencent-lexiang.png',
 'tencent-weiyun':'tencent-weiyun.png',
 'qq-mail':'qq-mail.png',
 'qq-music':'qq-music.png',
 'fliggy':'fliggy.png',
 'baidu-map':'baidu-map.png',
 'ctrip-wendao':'ctrip-wendao.png',
 'meituan-travel':'meituan-travel.png',
 'didi':'didi.png',
 'yuandian':'yuandian.svg',
 'qcc':'qcc.png',
 'tencent-ardot':'tencent-ardot.png',
 'youdao-note':'youdao-note.png',
 'notion':'notion.png',
 'openalex':'openalex.svg',
 'dida365':'dida365.png',
 'feishu-cli':'feishu-cli.svg',
 'wecom-cli':'wecom-cli.png',
 'dingtalk-cli':'dingtalk-cli.png',
 'agently-cli':'agently-cli.png',
 'obsidian-cli':'obsidian-cli.svg',
 'yuque-mcp':'yuque-mcp.svg',
 'weknora':'weknora.png',
 'dify':'dify.svg',
 'github':'github.svg',
 'gitee':'gitee.png',
 'gitlab':'gitlab.png',
 'codeup':'codeup.png',
 'context7':'context7.png',
 'linear':'linear.svg',
 'sentry':'sentry.png',
 'vercel':'vercel.png',
 'supabase':'supabase.png',
};

export function ConnectorIcon({catalogID,color}:{catalogID?:string;color?:string}){
 const logo=catalogID?connectorLogos[catalogID]:undefined;
 return <span className={`connector-preset-icon${logo?' connector-brand-icon':''}`} style={{color}} aria-hidden="true">{logo?<img src={`/connector-logos/${logo}`} alt="" width={28} height={28}/>:<Plug size={20}/>}</span>;
}
