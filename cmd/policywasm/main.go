//go:build js && wasm

// Command policywasm compiles the access decision to WebAssembly for the
// project page's live demo (SITE-004). It registers one global function,
// switchboardExplain(json) -> json, and answers nothing else: no network, no
// storage, no DOM. The decision is auth.NewReport, the code the edge runs.
package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/octaviosantacruz/switchboard/internal/policydemo"
)

// commit is stamped at build time (-X main.commit=...) so the page can say
// which engine it is running.
var commit = "unknown"

func explain(_ js.Value, args []js.Value) any {
	if len(args) != 1 || args[0].Type() != js.TypeString {
		return failure("switchboardExplain takes one JSON string")
	}
	out, err := policydemo.Explain([]byte(args[0].String()))
	if err != nil {
		return failure(err.Error())
	}
	return string(out)
}

func failure(message string) string {
	out, _ := json.Marshal(map[string]string{"error": message})
	return string(out)
}

func main() {
	js.Global().Set("switchboardExplain", js.FuncOf(explain))
	js.Global().Set("switchboardEngine", commit)
	// The functions above stay callable only while the program runs.
	select {}
}
