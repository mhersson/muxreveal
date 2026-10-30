package ui

// browserArgv is the program that opens raw and its only argument.
func browserArgv(raw string) ([]string, error) { return []string{"open", raw}, nil }
