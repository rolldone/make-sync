import{r as x}from"./index.Cd_vQiNd.js";var c={exports:{}},a={};/**
 * @license React
 * react-jsx-runtime.production.js
 *
 * Copyright (c) Meta Platforms, Inc. and affiliates.
 *
 * This source code is licensed under the MIT license found in the
 * LICENSE file in the root directory of this source tree.
 */var h;function f(){if(h)return a;h=1;var i=Symbol.for("react.transitional.element"),u=Symbol.for("react.fragment");function r(l,t,e){var o=null;if(e!==void 0&&(o=""+e),t.key!==void 0&&(o=""+t.key),"key"in t){e={};for(var s in t)s!=="key"&&(e[s]=t[s])}else e=t;return t=e.ref,{$$typeof:i,type:l,key:o,ref:t!==void 0?t:null,props:e}}return a.Fragment=u,a.jsx=r,a.jsxs=r,a}var m;function p(){return m||(m=1,c.exports=f()),c.exports}var n=p();function R(){const[i,u]=x.useState([]);return x.useEffect(()=>{const r=document.querySelector("article");if(!r)return;const l=r.querySelectorAll("h1, h2, h3, h4, h5, h6"),t=Array.from(l).map(e=>{const o=parseInt(e.tagName.charAt(1)),s=e.textContent||"",d=e.id||s.toLowerCase().replace(/\s+/g,"-").replace(/[^\w-]/g,"");return e.id=d,{id:d,text:s,level:o}});u(t)},[]),i.length===0?null:n.jsxs("div",{className:"mb-6",children:[n.jsx("h2",{className:"text-lg font-semibold mb-2",children:"Daftar Isi"}),n.jsx("nav",{children:n.jsx("ul",{className:"space-y-1",children:i.map(r=>n.jsx("li",{style:{paddingLeft:`${(r.level-1)*1}rem`},children:n.jsx("a",{href:`#${r.id}`,className:"text-sm text-muted-foreground hover:text-foreground transition-colors",children:r.text})},r.id))})})]})}export{R as TOC};
