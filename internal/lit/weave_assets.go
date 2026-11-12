package lit

const weaveCSS = `
:root{--fg:#111;--muted:#6f6f66;--rule:#cfcfc2;--code:transparent;--accent:#8a2a2a;--hl:#f4eecb;--paper:#fffff8;--bad:#a00;--hint-bg:transparent}
@media (prefers-color-scheme:dark){:root{--fg:#dedcd2;--muted:#9a988c;--rule:#45443d;--accent:#d99a8a;--hl:#3b3820;--paper:#151513;--bad:#f08a72}}
*{box-sizing:border-box}
::selection{background:var(--hl)}
html{font-size:16px}
body{margin:0;font:1.1rem/1.55 et-book,"Palatino Linotype","Book Antiqua",Palatino,"TeX Gyre Pagella",Georgia,serif;color:var(--fg);background:var(--paper);
 text-rendering:optimizeLegibility;font-variant-numeric:oldstyle-nums}
main{max-width:41rem;margin:0 auto;padding:3rem 1.4rem 6rem}
a{color:inherit;text-decoration:underline;text-decoration-thickness:.05em;text-decoration-color:var(--muted);text-underline-offset:.18em}
a:hover{color:var(--accent);text-decoration-color:var(--accent)}
h1,h2,h3{font-family:inherit;font-weight:400;font-style:italic;line-height:1.15;color:var(--fg)}
figure.fig{margin:1.6rem 0;text-align:center;color:var(--fg)}figure.fig svg,figure.fig img{max-width:100%;height:auto}
figcaption{color:var(--muted);font-size:.9rem;margin-top:.4rem}
h1.title{font-style:normal;font-size:2.6rem;margin:.4rem 0 .3rem}.sub{color:var(--muted);font-style:italic;font-size:1.2rem;margin-top:0}
h2{font-size:1.8rem;margin:3.2rem 0 1rem;border:0}h3{font-size:1.4rem;margin:2.2rem 0 .8rem}
code,pre,.fname{font-family:"Latin Modern Mono",ui-monospace,"DejaVu Sans Mono",Menlo,Consolas,monospace}
code{background:none;padding:0;font-size:.86em}
.display{margin:.8rem 0;overflow-x:auto}
.cite a{font-size:.92em}.cite{white-space:nowrap}
.chunk{margin:2.2rem 0;padding:0}
.chunk.nested{margin:1.4rem 0 1.4rem .9rem;padding-left:.9rem;border-left:1px solid var(--rule)}
.chunk:target>.chunk-head{background:var(--hl)}
.chunk-head{font-size:1.02rem;padding:.1rem .3rem;margin-left:-.3rem}
.chunk-head .num{color:var(--muted);margin-right:.4rem}
.cname{font-style:normal;font-family:"Latin Modern Mono",ui-monospace,"DejaVu Sans Mono",monospace;font-size:.88em}.eq{color:var(--muted)}
.badge{font-variant:small-caps;letter-spacing:.06em;color:var(--muted);font-size:.9rem;font-weight:400}
.fname{font-weight:400}
.meta{font-style:italic;font-size:.82rem;line-height:1.4;color:var(--muted);margin:.1rem 0 .6rem}
pre.code{background:none;border:0;border-left:1px solid var(--rule);border-radius:0;padding:.15rem 0 .15rem 1rem;white-space:pre-wrap;overflow-wrap:anywhere;
 font-size:.8rem;line-height:1.45;margin:.9rem 0;tab-size:4}
a.ref{font-weight:400}.ref.bad{color:var(--bad)}.rn{color:var(--muted);font-size:.8em;margin-left:.15rem}
.hint{border-left:2px solid var(--accent);padding:.2rem .9rem;font-style:italic;margin:1rem 0}
table.index{border-collapse:collapse;width:100%;font-size:.9rem;border-top:2px solid var(--fg);border-bottom:2px solid var(--fg)}
.index th{border-bottom:1px solid var(--fg);padding:.3rem .5rem;text-align:left;font-weight:400;font-style:italic}
.index td{padding:.2rem .5rem;text-align:left;vertical-align:top}
.index td.n{white-space:nowrap;color:var(--muted)}
ol.bib{padding-left:2.2rem;font-size:.95rem}ol.bib li{margin:.5rem 0}
nav ul{list-style:none;padding:0;margin:0}
nav#contents{border-top:1px solid var(--rule);border-bottom:1px solid var(--rule);padding:.5rem 0;margin:1.6rem 0;font-size:.95rem}
nav#contents h2{border:0;margin:.4rem 0;font-size:1.3rem}nav#contents h3{margin:.8rem 0 .2rem;font-size:1rem}
nav li.l2{margin-left:.9rem}nav li{margin:.12rem 0}
nav#side{display:none}
@media screen and (min-width:1180px){
 nav#side{display:block;position:fixed;top:0;left:0;bottom:0;width:17rem;overflow-y:auto;padding:1.6rem 1.1rem;
  border-right:1px solid var(--rule);font-size:.88rem;line-height:1.4}
 nav#side a{text-decoration:none}nav#side a:hover{text-decoration:underline}
 nav#side .st{font-style:italic;margin:.7rem 0 .3rem;color:var(--muted)}nav#side li.l2{margin-left:.7rem}
 main{margin-left:19rem;margin-right:0;max-width:44rem;padding-left:2.5rem;padding-right:2.5rem}nav#contents{display:none}}
@media screen and (min-width:1340px){
 main{max-width:none;padding-right:3rem}
 main>*{max-width:38rem}
 .chunk{position:relative}
 .meta{float:right;clear:right;width:14.5rem;margin:.2rem -16.5rem .6rem 0;font-size:.8rem}
 .chunk.nested .meta{margin-right:-17.4rem}.chunk.nested .chunk.nested .meta{margin-right:-18.3rem}}
@media print{body{background:#fff}nav#side{display:none}main{max-width:none;margin:0;padding:0}
 pre.code{break-inside:avoid;white-space:pre-wrap}h1,h2,h3,.chunk-head,.meta{break-after:avoid}}
`

const texPreamble = `\documentclass[10pt,a4paper]{article}
\usepackage{iftex}
\ifPDFTeX
  \usepackage[utf8]{inputenc}
  \usepackage[T1]{fontenc}
  \IfFileExists{lmodern.sty}{\usepackage{lmodern}}{}
\else
  \usepackage{fontspec}
  \IfFontExistsTF{TeX Gyre Pagella}{\setmainfont{TeX Gyre Pagella}}{\IfFontExistsTF{texgyrepagella-regular.otf}{\setmainfont{texgyrepagella-regular.otf}[BoldFont=texgyrepagella-bold.otf,ItalicFont=texgyrepagella-italic.otf,BoldItalicFont=texgyrepagella-bolditalic.otf]}{\IfFontExistsTF{STIX Two Text}{\setmainfont{STIX Two Text}}{}}}
  \IfFontExistsTF{DejaVu Sans Mono}{\setmonofont{DejaVu Sans Mono}[Scale=MatchLowercase]}{}
\fi
\usepackage[a4paper,left=24mm,textwidth=118mm,marginparwidth=58mm,marginparsep=6mm,top=24mm,bottom=26mm]{geometry}
\usepackage{amsmath,amssymb}
\usepackage[dvipsnames]{xcolor}
\definecolor{tufteink}{HTML}{111111}
\definecolor{tuftepaper}{HTML}{FFFFF8}
\definecolor{tufteaccent}{HTML}{8A2A2A}
\definecolor{tuftegray}{HTML}{6F6F66}
\AtBeginDocument{\pagecolor{tuftepaper}\color{tufteink}}
\usepackage{fancyvrb}
\IfFileExists{fvextra.sty}{\usepackage{fvextra}\fvset{breaklines=true}}{}
\usepackage{longtable}
\usepackage{graphicx}
\usepackage[colorlinks=true,linkcolor=tufteaccent,citecolor=tufteaccent,urlcolor=tufteaccent]{hyperref}
\fvset{commandchars=\\\{\},fontsize=\scriptsize,frame=leftline,framesep=3mm,rulecolor=\color{tuftegray!45}}
@@STYLE@@
\setlength{\parindent}{0pt}
\setlength{\parskip}{0.55em plus 0.15em}
\linespread{1.06}
\emergencystretch=2em
\makeatletter
\renewcommand\section{\@startsection{section}{1}{\z@}{-3.2ex \@plus -1ex \@minus -.2ex}{1.2ex \@plus .2ex}{\normalfont\LARGE\itshape}}
\renewcommand\subsection{\@startsection{subsection}{2}{\z@}{-2.6ex \@plus -1ex \@minus -.2ex}{1ex \@plus .2ex}{\normalfont\Large\itshape}}
\renewcommand\subsubsection{\@startsection{subsubsection}{3}{\z@}{-2.2ex \@plus -1ex \@minus -.2ex}{.8ex \@plus .2ex}{\normalfont\large\itshape}}
\makeatother
\newcounter{chunk}
\renewcommand{\thechunk}{\S\arabic{chunk}}
\newcommand{\chunkhead}[2]{\par\addvspace{1.2ex}\noindent\refstepcounter{chunk}\label{#1}{\bfseries\thechunk}\enspace #2\par\nopagebreak}
\newcommand{\chunkmeta}[1]{\marginpar{\raggedright\scriptsize\itshape\color{tuftegray}#1}\nopagebreak}
\newcommand{\chunkname}[1]{\ensuremath{\langle}\texttt{#1}\ensuremath{\rangle}}
\newcommand{\chunklink}[2]{\hyperref[#1]{#2~\ref*{#1}}\ (p.~\pageref*{#1})}
\newcommand{\chunkrefv}[1]{\hyperref[#1]{\ensuremath{\langle}\csname cn@#1\endcsname\ensuremath{\rangle}\textsuperscript{\ref*{#1}}}}
\newcommand{\filebadge}{{\footnotesize\itshape\color{tuftegray}file}}
\newenvironment{nested}{\begin{list}{}{\setlength{\leftmargin}{1.1em}\setlength{\rightmargin}{0pt}\setlength{\topsep}{0pt}\setlength{\partopsep}{0pt}\setlength{\parsep}{\parskip}\setlength{\itemsep}{0pt}}\item[]}{\end{list}}
\hypersetup{pdftitle={@@PDFTITLE@@}}
\title{@@TITLE@@\\[0.4em]\large Woven from \texttt{@@SOURCE@@}}
\author{}
\date{}
@@NAMES@@
\begin{document}
\maketitle
\tableofcontents
`

const mathJax = "https://cdn.jsdelivr.net/npm/mathjax@3.2.2/es5/tex-svg.js"
