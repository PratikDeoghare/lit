package brashtag

// ParseRest parses one bag from the start of text and returns it together with the text that follows it.
// On a syntax error it returns the text from the place where parsing stopped, so that a caller can say where
// the error is. (Parse, in parser.go, reports only the first 100 bytes of what is left.)
func ParseRest(text string) (Bag, string, error) {
	n, rest, err := parseBag([]byte(text))
	if err != nil {
		return Bag{}, string(rest), err
	}
	return n.(Bag), string(rest), nil
}
