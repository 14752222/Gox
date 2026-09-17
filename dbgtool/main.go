package main

import (
	"fmt"

	"github.com/14752222/Gox/lexer"
)

func main() {
	l := lexer.New(`const a = null ?? 5;`)
	for {
		t := l.NextToken()
		fmt.Printf("%-3d %-18s %q\n", t.Type, t.Type.String(), t.Literal)
		if t.Type == lexer.EOF {
			break
		}
	}
}
