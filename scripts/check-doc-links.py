"""Repoint every [`name`](path#Lnn) link in the docs at name's current
definition line, and check every link resolves. Run from the repo root."""
import os
import re
import sys

docs = ['README.md'] + ['docs/' + f for f in sorted(os.listdir('docs')) if f.endswith('.md')]
cache = {}


def lines(p):
    if p not in cache:
        cache[p] = open(p, encoding='utf-8').read().splitlines()
    return cache[p]


def find(path, text):
    name = text.strip('`').split('(')[0].strip()
    parts = name.split('.')
    fn = parts[-1]
    recv = parts[-2] if len(parts) >= 2 and parts[-2][:1].isupper() else None
    L = lines(path)
    pats = []
    if recv:
        pats.append(rf'^func \(\w+ \*?{re.escape(recv)}\) {re.escape(fn)}\(')
    pats += [rf'^func (\(\w+ \*?\w+\) )?{re.escape(fn)}\(', rf'^type {re.escape(fn)} ', rf'^var {re.escape(fn)} ',
             rf'^\s*{re.escape(fn)}\s*=']
    for p in pats:
        hits = [i + 1 for i, l in enumerate(L) if re.search(p, l)]
        if len(hits) == 1:
            return hits[0]
        if len(hits) > 1:
            return ('ambiguous', hits)
    return None


def slugs(path):
    out, inside = set(), False
    for l in open(path, encoding='utf-8'):
        if l.startswith('```'):
            inside = not inside
            continue
        if inside:
            continue
        m = re.match(r'^(#{1,6})\s+(.*)', l)
        if m:
            t = re.sub(r'[^\w\- ]', '', m.group(2).strip().lower())
            out.add(t.replace(' ', '-'))
    return out


changed, problems = 0, []
for d in docs:
    s = open(d, encoding='utf-8').read()
    base = os.path.dirname(d)

    def fix(m):
        global changed
        text, path, line = m.group(1), m.group(2), int(m.group(3))
        full = os.path.normpath(os.path.join(base, path))
        if not full.endswith('.go') or text.strip('`').endswith('.go'):
            return m.group(0)
        got = find(full.replace('\\', '/'), text)
        if isinstance(got, int):
            if got != line:
                changed += 1
                return f'[{text}]({path}#L{got})'
            return m.group(0)
        problems.append((d, text, path, got))
        return m.group(0)

    s2 = re.sub(r'\[([^\]]+)\]\(((?:\.\./)?[^)#\s]+)#L(\d+)\)', fix, s)
    if s2 != s:
        open(d, 'w', encoding='utf-8', newline='\n').write(s2)

total = 0
for d in docs:
    s = re.sub(r'```.*?```', '', open(d, encoding='utf-8').read(), flags=re.S)
    base = os.path.dirname(d)
    for text, target in re.findall(r'\[([^\]]*)\]\(([^)\s]+)\)', s):
        if target.startswith('http'):
            continue
        total += 1
        path, _, anchor = target.partition('#')
        full = os.path.normpath(os.path.join(base, path)) if path else d
        if not os.path.exists(full):
            problems.append((d, 'NO FILE', target))
            continue
        if anchor and not re.fullmatch(r'L\d+', anchor) and anchor not in slugs(full):
            problems.append((d, 'NO HEADING', target))
print(f'{changed} line links repointed; {total} links checked')
for p in problems:
    print('PROBLEM', p)
sys.exit(1 if problems else 0)
