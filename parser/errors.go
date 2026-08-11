package parser

import (
	"fmt"
	"strings"
)

// ParseError 表示解析过程中发现的错误。
type ParseError struct {
	Message string
	Line    int
	Column  int
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("line %d:%d: %s", e.Line, e.Column, e.Message)
}

// ErrorList 存储解析过程中收集的所有错误。
type ErrorList struct {
	Errors []*ParseError
}

func NewErrorList() *ErrorList {
	return &ErrorList{Errors: []*ParseError{}}
}

func (el *ErrorList) Add(msg string, line, col int) {
	el.Errors = append(el.Errors, &ParseError{
		Message: msg,
		Line:    line,
		Column:  col,
	})
}

func (el *ErrorList) HasErrors() bool {
	return len(el.Errors) > 0
}

func (el *ErrorList) String() string {
	if !el.HasErrors() {
		return ""
	}
	var lines []string
	for _, e := range el.Errors {
		lines = append(lines, e.Error())
	}
	return strings.Join(lines, "\n")
}
