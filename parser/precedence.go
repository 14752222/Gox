package parser

// Precedence 表示运算符优先级。
// 数值越大，优先级越高。
type Precedence int

const (
	LOWEST    Precedence = iota // 0: 最低 (默认)
	COMMA_SEQ                   // 1: 逗号运算符 (a, b) (最低实际运算符)
	ASSIGN                      // 2: = += -= 等 (右结合)
	TERNARY                     // 3: ? : (条件表达式, 右结合)
	OR                          // 4: ||
	NULLISH                     // 5: ?? (nullish coalescing)
	AND                         // 6: &&
	BIT_OR                      // 7: |
	BIT_XOR                     // 8: ^
	BIT_AND                     // 9: &
	EQUALITY                    // 10: == != === !==
	COMPARE                     // 11: < > <= >= instanceof in
	SHIFT                       // 12: << >> >>>
	ADD                         // 13: + -
	MULT                        // 14: * / %
	EXPONENT                    // 15: ** (右结合)
	UNARY                       // 16: ! - + typeof ++ -- (前缀)
	CALL                        // 17: f() a[b]
	MEMBER                      // 18: a.b (最高)
	POSTFIX                     // 19: x++ x-- (后缀，最高)
)

// ternaryOperand 是条件表达式两个分支的解析层级。
//
// 规范里 Consequent/Alternative 都是 AssignmentExpression, 也就是"比逗号紧、
// 比赋值松"的那一整层 —— 落在本表里就是 COMMA_SEQ。用这一层而不是 TERNARY-1
// (即 ASSIGN) 有两个区别, 两者都更贴近规范:
//   - `a ? b : c = d` 解析为 `a ? b : (c = d)` 而非 `(a ? b : c) = d`;
//   - `a ? b = c : d` 能正常解析, 而用 ASSIGN 层会在 `=` 处直接报错。
//
// 逗号本身由 parseExpression 循环里的显式 COMMA 守卫拦住, 不会被分支吞掉。
const ternaryOperand = COMMA_SEQ

// precedences 将中缀运算符令牌映射到优先级。
var precedences = map[string]Precedence{
	",":          COMMA_SEQ,
	"=":          ASSIGN,
	"+=":         ASSIGN,
	"-=":         ASSIGN,
	"*=":         ASSIGN,
	"/=":         ASSIGN,
	"%=":         ASSIGN,
	"**=":        ASSIGN,
	"&&=":        ASSIGN,
	"||=":        ASSIGN,
	"&=":         ASSIGN,
	"|=":         ASSIGN,
	"^=":         ASSIGN,
	"<<=":        ASSIGN,
	">>=":        ASSIGN,
	">>>=":       ASSIGN,
	"??=":        ASSIGN, // nullish coalescing assignment (低优先级)
	"??":         NULLISH,
	"?":          TERNARY,
	"||":         OR,
	"&&":         AND,
	"|":          BIT_OR,
	"^":          BIT_XOR,
	"&":          BIT_AND,
	"==":         EQUALITY,
	"!=":         EQUALITY,
	"===":        EQUALITY,
	"!==":        EQUALITY,
	"<":          COMPARE,
	">":          COMPARE,
	"<=":         COMPARE,
	">=":         COMPARE,
	"instanceof": COMPARE,
	"in":         COMPARE,
	"<<":         SHIFT,
	">>":         SHIFT,
	">>>":        SHIFT,
	"+":          ADD,
	"-":          ADD,
	"*":          MULT,
	"/":          MULT,
	"%":          MULT,
	"**":         EXPONENT,
	"++":         POSTFIX,
	"--":         POSTFIX,
	"(":          CALL,
	"[":          MEMBER,
	".":          MEMBER,
	"?.":         MEMBER,
}

// getPrecedence 返回令牌字面量对应的优先级。
// 如果令牌不在映射中，返回 LOWEST。
func getPrecedence(literal string) Precedence {
	if p, ok := precedences[literal]; ok {
		return p
	}
	return LOWEST
}
