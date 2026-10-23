// rpttree prints the TSLV record tree of a Crystal .rpt: streams, then
// records with tag, schema, size and any readable strings.
//
//	rpttree [-depth 3] [-strings] [-hex 102] file.rpt
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"

	"github.com/iamnikolie/rptgo/rpt"
)

func main() {
	depth := flag.Int("depth", 1, "nesting depth to print")
	showStrings := flag.Bool("strings", false, "print printable strings found in each record")
	hexTag := flag.Int("hex", -1, "dump the bytes of every record with this tag, at any depth")
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	f, err := rpt.Open(flag.Arg(0))
	if err != nil {
		fail(err)
	}
	names := make([]string, 0, len(f.Streams))
	for n := range f.Streams {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Printf("stream %-30q %7d bytes\n", n, len(f.Streams[n]))
	}
	payload, err := f.Contents()
	if err != nil {
		fail(err)
	}
	fmt.Printf("Contents: encrypted=%v inflated=%d bytes\n", rpt.IsEncrypted(f.Streams["Contents"]), len(payload))
	recs := rpt.Records(payload)
	fmt.Printf("%d top-level records\n", len(recs))
	if *hexTag >= 0 {
		dump(recs, *hexTag, "")
		return
	}
	walk(recs, 0, *depth, *showStrings)
}

// dump prints the bytes of every record with the tag, with its path of tags.
func dump(recs []rpt.Record, tag int, path string) {
	for _, r := range recs {
		p := fmt.Sprintf("%s/%d", path, r.Tag)
		if r.Tag == tag {
			fmt.Printf("%s [%d]\n% x\n", p, len(r.Data), r.Data)
		}
		dump(r.Children(), tag, p)
	}
}

func walk(recs []rpt.Record, level, depth int, showStrings bool) {
	for _, r := range recs {
		line := fmt.Sprintf("%s%d", strings.Repeat("  ", level), r.Tag)
		if r.Schema != 0 {
			line += fmt.Sprintf(" s%d", r.Schema)
		}
		line += fmt.Sprintf(" [%d]", len(r.Data))
		if showStrings {
			if s := printable(r.Data); s != "" {
				line += "  " + s
			}
		}
		fmt.Println(line)
		if level+1 < depth {
			walk(r.Children(), level+1, depth, showStrings)
		}
	}
}

// printable returns the runs of 3+ printable ASCII characters, quoted.
func printable(b []byte) string {
	var runs []string
	var cur []byte
	flush := func() {
		if len(cur) >= 3 {
			runs = append(runs, fmt.Sprintf("%q", string(cur)))
		}
		cur = cur[:0]
	}
	for _, c := range b {
		if c < 0x80 && unicode.IsPrint(rune(c)) {
			cur = append(cur, c)
		} else {
			flush()
		}
	}
	flush()
	s := strings.Join(runs, " ")
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "rpttree:", err)
	os.Exit(1)
}
