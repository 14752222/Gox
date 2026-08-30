package parser

// Precedence 表示运算符优先级。
// 数值越大，优先级越高。
type Precedence int

const (
	LOWEST      Precedence = iota // 0: 最低 (默认)
	COMMA_SEQ                     // 1: 逗号运算符 (a, b) (最低实际运算符)
	ASSIGN                        // 2: = += -= 等 (右结合)
	TERNARY                       // 2: ? : (条件表达式)
	OR                            // 3: ||
	NULLISH                       // 4: ?? (nullish coalescing)
	AND                           // 5: &&
	BIT_OR                        // 6: |
	BIT_XOR                       // 7: ^
	BIT_AND                       // 8: &
	EQUALITY                      // 9: == != === !==
	COMPARE                       // 10: < > <= >= instanceof in
	SHIFT                         // 11: << >> >>>
	ADD                           // 12: + -
	MULT                          // 13: * / %
	EXPONENT                      // 14: ** (右结合)
	UNARY                         // 15: ! - + typeof ++ -- (前缀)
	CALL                          // 16: f() a[b]
	MEMBER                        // 17: a.b (最高)
	POSTFIX                       // 18: x++ x-- (后缀，最高)
)

// precedences 将中缀运算符令牌映射到优先级。
var precedences = map[string]Precedence{
	",":    COMMA_SEQ,
	"=":    ASSIGN,
	"+=":   ASSIGN,
	"-=":   ASSIGN,
	"*=":   ASSIGN,
	"/=":   ASSIGN,
	"%=":   ASSIGN,
	"**=":  ASSIGN,
	"&&=":  ASSIGN,
	"||=":  ASSIGN,
	"&=":   ASSIGN,
	"|=":   ASSIGN,
	"^=":   ASSIGN,
	"??=":  ASSIGN, // nullish coalescing assignment (低优先级)
	"??":   NULLISH,
	"?":    TERNARY,
	"||":   OR,
	"&&":   AND,
	"|":    BIT_OR,
	"^":    BIT_XOR,
	"&":    BIT_AND,
	"==":   EQUALITY,
	"!=":   EQUALITY,
	"===":  EQUALITY,
	"!==":  EQUALITY,
	"<":    COMPARE,
	">":    COMPARE,
	"<=":   COMPARE,
	">=":   COMPARE,
	"instanceof": COMPARE,
	"in":    COMPARE,
	"<<":   SHIFT,
	">>":   SHIFT,
	">>>":  SHIFT,
	"+":    ADD,
	"-":    ADD,
	"*":    MULT,
	"/":    MULT,
	"%":    MULT,
	"**":   EXPONENT,
	"++":   POSTFIX,
	"--":   POSTFIX,
	"(":    CALL,
	"[":    MEMBER,
	".":    MEMBER,
	"?.":   MEMBER,
}

// getPrecedence 返回令牌字面量对应的优先级。
// 如果令牌不在映射中，返回 LOWEST。
func getPrecedence(literal string) Precedence {
	if p, ok := precedences[literal]; ok {
		return p
	}
	return LOWEST
}
