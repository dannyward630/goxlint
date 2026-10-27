package visitor

var bad = 1 // want "avoid bad"

func suppressed() {
	//goxlint:ignore acme/no-bad this binding is intentional
	bad := 1
	_ = bad //goxlint:ignore acme/no-bad this use is intentional
}

//goxlint:ignore another/rule this is outside the analyzer's scope
var good = 1
