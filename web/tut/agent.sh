#!/bin/sh
# A canned answer standing in for a model: it recognises which gap it is asked about.
p=$(cat)
case "$p" in
*"Chunk to fill: «read the words»"*)
cat <<'X'
```
sc := bufio.NewScanner(os.Stdin)
for sc.Scan() {
	for _, w := range strings.Fields(sc.Text()) {
		counts[strings.ToLower(w)]++
	}
}
```
X
;;
*)
cat <<'X'
```
type kv struct {
	word string
	n    int
}
var all []kv
for w, n := range counts {
	all = append(all, kv{w, n})
}
sort.Slice(all, func(i, j int) bool {
	if all[i].n != all[j].n {
		return all[i].n > all[j].n
	}
	return all[i].word < all[j].word
})
for i := 0; i < len(all) && i < 10; i++ {
	fmt.Println(all[i].n, all[i].word)
}
```
X
;;
esac
