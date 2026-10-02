package stdlib

import (
	"fmt"
	"os"
	"strings"

	"github.com/14752222/Gox/object"
)

// setupConsole 创建 console 对象。
func setupConsole() *object.Object {
	console := object.NewObject()

	console.SetProperty("log", object.NewBuiltin("log", func(args ...object.Value) object.Value {
		var parts []string
		for _, arg := range args {
			if arg == nil {
				parts = append(parts, "undefined")
			} else {
				parts = append(parts, arg.Inspect())
			}
		}
		msg := strings.Join(parts, " ")
		fmt.Fprintln(os.Stdout, msg)
		object.RecordDevLog("log", msg) // devtools 留存 (行为不变; 见 object/devlog.go)
		return object.UndefinedSingleton
	}))

	console.SetProperty("error", object.NewBuiltin("error", func(args ...object.Value) object.Value {
		var parts []string
		for _, arg := range args {
			if arg == nil {
				parts = append(parts, "undefined")
			} else {
				parts = append(parts, arg.Inspect())
			}
		}
		msg := strings.Join(parts, " ")
		fmt.Fprintln(os.Stderr, msg)
		object.RecordDevLog("error", msg) // devtools 留存 (行为不变; 见 object/devlog.go)
		return object.UndefinedSingleton
	}))

	console.SetProperty("warn", object.NewBuiltin("warn", func(args ...object.Value) object.Value {
		var parts []string
		for _, arg := range args {
			if arg == nil {
				parts = append(parts, "undefined")
			} else {
				parts = append(parts, arg.Inspect())
			}
		}
		msg := strings.Join(parts, " ")
		fmt.Fprintln(os.Stderr, msg)
		object.RecordDevLog("warn", msg) // devtools 留存 (行为不变; 见 object/devlog.go)
		return object.UndefinedSingleton
	}))

	console.SetProperty("info", object.NewBuiltin("info", func(args ...object.Value) object.Value {
		var parts []string
		for _, arg := range args {
			if arg == nil {
				parts = append(parts, "undefined")
			} else {
				parts = append(parts, arg.Inspect())
			}
		}
		msg := strings.Join(parts, " ")
		fmt.Fprintln(os.Stdout, msg)
		object.RecordDevLog("info", msg) // devtools 留存 (行为不变; 见 object/devlog.go)
		return object.UndefinedSingleton
	}))

	return console
}
