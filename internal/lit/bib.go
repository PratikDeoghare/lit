package lit

import (
	"regexp"
	"strings"
)

// accented maps an accent command and a base letter to the precomposed letter. The standard library has no
// Unicode normalisation, so the common Latin letters are listed here; others keep the combining mark.
var accented = func() map[string]rune {
	m := map[string]rune{}
	add := func(acc, bases, composed string) {
		b, c := []rune(bases), []rune(composed)
		for i := range b {
			m[acc+string(b[i])] = c[i]
		}
	}
	add("'", "AEIOUYaeiouyCcNnSsZzLlRr", "\u00c1\u00c9\u00cd\u00d3\u00da\u00dd\u00e1\u00e9\u00ed\u00f3\u00fa\u00fd\u0106\u0107\u0143\u0144\u015a\u015b\u0179\u017a\u0139\u013a\u0154\u0155")
	add("`", "AEIOUaeiou", "\u00c0\u00c8\u00cc\u00d2\u00d9\u00e0\u00e8\u00ec\u00f2\u00f9")
	add("\"", "AEIOUaeiouyY", "\u00c4\u00cb\u00cf\u00d6\u00dc\u00e4\u00eb\u00ef\u00f6\u00fc\u00ff\u0178")
	add("^", "AEIOUaeiouCcGgHhJjSsWwYy", "\u00c2\u00ca\u00ce\u00d4\u00db\u00e2\u00ea\u00ee\u00f4\u00fb\u0108\u0109\u011c\u011d\u0124\u0125\u0134\u0135\u015c\u015d\u0174\u0175\u0176\u0177")
	add("~", "ANOanoIiUu", "\u00c3\u00d1\u00d5\u00e3\u00f1\u00f5\u0128\u0129\u0168\u0169")
	return m
}()

var combining = map[string]string{"'": "\u0301", "`": "\u0300", "\"": "\u0308", "^": "\u0302", "~": "\u0303"}

var (
	accentRe = regexp.MustCompile("\\\\(['`\"^~])\\{?(\\w)\\}?")
	braceRe  = regexp.MustCompile(`[{}]`)
)

func texText(s string) string {
	s = accentRe.ReplaceAllStringFunc(s, func(m string) string {
		g := accentRe.FindStringSubmatch(m)
		if r, ok := accented[g[1]+g[2]]; ok {
			return string(r)
		}
		return g[2] + combining[g[1]]
	})
	s = strings.ReplaceAll(s, "--", "\u2013")
	s = strings.ReplaceAll(s, `\&`, "&")
	return strings.TrimSpace(braceRe.ReplaceAllString(s, ""))
}

var (
	bibHead  = regexp.MustCompile(`^\s*@(\w+)\s*\{\s*([^,]+),`)
	bibField = regexp.MustCompile(`\s*(\w+)\s*=\s*`)
	bibBare  = regexp.MustCompile(`^[^,}\s]+`)
	andRe    = regexp.MustCompile(`\s+and\s+`)
	urlRe    = regexp.MustCompile(`\\url\{([^}]+)\}`)
	arxivRe  = regexp.MustCompile(`arXiv:([\w./-]+)`)
	arxivSfx = regexp.MustCompile(`\s*arXiv:\S+`)
)

func parseBibtex(src string) map[string]string {
	pos := 0
	if m := bibHead.FindStringIndex(src); m != nil {
		pos = m[1]
	}
	fields := map[string]string{}
	for {
		loc := bibField.FindStringSubmatchIndex(src[pos:])
		if loc == nil {
			break
		}
		name := strings.ToLower(src[pos+loc[2] : pos+loc[3]])
		p := pos + loc[1]
		var val string
		if p < len(src) && src[p] == '{' {
			depth, j := 0, p
			for j < len(src) {
				switch src[j] {
				case '{':
					depth++
				case '}':
					depth--
				}
				if depth == 0 {
					break
				}
				j++
			}
			val, pos = src[p+1:min(j, len(src))], j+1
		} else {
			val = bibBare.FindString(src[p:])
			pos = p + len(val)
		}
		fields[name] = val
	}
	return fields
}

type bibInfo struct{ authors, title, venue, vol, pages, year, url, hp, arxiv string }

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func bibParts(f map[string]string) bibInfo {
	var names []string
	for _, n := range andRe.Split(texText(f["author"]), -1) {
		names = append(names, strings.TrimSpace(n))
	}
	authors := ""
	switch {
	case len(names) > 0 && names[len(names)-1] == "others":
		authors = strings.Join(names[:len(names)-1], ", ") + " et al."
	case len(names) > 1:
		authors = strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	case len(names) > 0:
		authors = names[0]
	}
	venue := arxivSfx.ReplaceAllString(first(f["journal"], f["booktitle"], f["institution"], f["publisher"]), "")
	hp := f["howpublished"]
	um := urlRe.FindStringSubmatch(hp)
	am := arxivRe.FindStringSubmatch(f["note"] + " " + f["journal"])
	vol := f["volume"]
	if f["number"] != "" {
		vol += "(" + f["number"] + ")"
	}
	b := bibInfo{authors: authors, title: texText(f["title"]), venue: texText(venue), vol: vol,
		pages: texText(f["pages"]), year: f["year"]}
	if um != nil {
		b.url = um[1]
	} else {
		b.hp = texText(hp)
	}
	if am != nil {
		b.arxiv = am[1]
	}
	return b
}

// stop adds a full stop unless the text already ends in punctuation.
func stop(s string) string {
	if s != "" && strings.ContainsRune(".?!", rune(s[len(s)-1])) {
		return s
	}
	return s + "."
}
