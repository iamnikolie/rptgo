// rpt2json parses a Crystal .rpt and writes its report model as JSON, in the
// shape the C# reference reader (majorsilence.crystal) writes.
//
//	rpt2json file.rpt > file.json
package main

import (
	"fmt"
	"os"

	"github.com/iamnikolie/rptgo/rpt"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: rpt2json file.rpt")
		os.Exit(2)
	}
	res := rpt.Parse(os.Args[1])
	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stderr, "WARN:", w)
	}
	if !res.Success || res.Report == nil {
		for _, e := range res.Errors {
			fmt.Fprintln(os.Stderr, "ERROR:", e)
		}
		os.Exit(1)
	}
	b, err := rpt.MarshalIndent(res.Report)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rpt2json:", err)
		os.Exit(1)
	}
	os.Stdout.Write(b)
}
