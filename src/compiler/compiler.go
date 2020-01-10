package compiler

import (
	"fmt"
	"github.com/adamjedlicka/go-blue/src/parser"
	"github.com/adamjedlicka/go-blue/src/value"
	"os"
	"strconv"
)

type Compiler struct {
	p     *parser.Parser
	chunk *Chunk

	hadError  bool
	panicMode bool
}

func NewCompiler(source string) Compiler {
	return Compiler{
		p:     parser.NewParser(source),
		chunk: NewChunk(),

		hadError:  false,
		panicMode: false,
	}
}

func (c *Compiler) Compile() *Chunk {
	for true {
		c.advance()

		if !c.check(parser.Newline) {
			break
		}
	}

	for !c.match(parser.Eof) {
		c.declaration()
	}

	// Patch last Pop for REPL
	if c.chunk.code[len(c.chunk.code)-1] == uint8(Pop) {
		c.chunk.code[len(c.chunk.code)-1] = uint8(Return)
	}

	if c.hadError {
		return nil
	}

	return c.chunk
}

func (c *Compiler) declaration() {
	c.statement()
}

func (c *Compiler) statement() {
	if c.match(parser.Return) {
		c.returnStatement()
	} else {
		c.expressionStatement()
	}
}

func (c *Compiler) expression() {
	c.parsePrecedence(PrecedenceAssignment)
}

func (c *Compiler) returnStatement() {
	if c.match(parser.Newline) {
		c.emitReturn()
	} else {
		needsNewline := !c.check(parser.Fn)

		c.expression()
		c.emitOpCode(Return)

		if needsNewline {
			c.expectNewlineOrSemicolon()
		}
	}
}

func (c *Compiler) expressionStatement() {
	c.expression()

	c.emitOpCode(Pop)

	c.expectNewlineOrSemicolon()
}

func (c *Compiler) parsePrecedence(precedence Precedence) {
	c.advance()

	prefixRule := parseRules[c.p.Previous().Type()].prefix
	if prefixRule == nil {
		c.error("Expect expression.")

		return
	}

	canAssign := precedence < PrecedenceAssignment
	prefixRule(c, canAssign)

	for precedence < parseRules[c.p.Current().Type()].precedence {
		c.advance()

		infixRule := parseRules[c.p.Previous().Type()].infix
		infixRule(c, canAssign)
	}

	if canAssign && c.match(parser.Equal) {
		c.error("Invalid assignment target.")

		// Parse the expression so compiler prints propper error messages.
		c.expression()
	}
}

func (c *Compiler) unary(canAssign bool) {
	operatorType := c.p.Previous().Type()

	c.parsePrecedence(PrecedenceUnary)

	switch operatorType {
	case parser.Bang:
		c.emitOpCode(Not)
	case parser.Minus:
		c.emitOpCode(Negate)
	default:
		panic("unreachable")
	}
}

func (c *Compiler) binary(canAssign bool) {
	operatorType := c.p.Previous().Type()

	rule := parseRules[operatorType]

	c.parsePrecedence(rule.precedence + 1)

	switch operatorType {
	case parser.EqualEqual:
		c.emitOpCode(Equal)
	case parser.Greater:
		c.emitOpCode(Greater)
	case parser.GreaterEqual:
		c.emitOpCode(GreaterEqual)
	case parser.Less:
		c.emitOpCode(Less)
	case parser.LessEqual:
		c.emitOpCode(LessEqual)
	case parser.BangEqual:
		c.emitOpCode(NotEqual)

	case parser.Plus:
		c.emitOpCode(Add)
	case parser.Minus:
		c.emitOpCode(Subtract)
	case parser.Slash:
		c.emitOpCode(Divide)
	case parser.Star:
		c.emitOpCode(Multiply)
	case parser.Caret:
		c.emitOpCode(Exponentiate)
	case parser.Percent:
		c.emitOpCode(Reminder)
	}
}

func (c *Compiler) number(canAssign bool) {
	lexeme := c.p.Previous().Lexeme()
	number, err := strconv.ParseFloat(lexeme, 64)
	if err != nil {
		panic(err)
	}

	c.emitConstant(value.Number(number))
}

func (c *Compiler) string(canAssign bool) {
	lexeme := c.p.Previous().Lexeme()
	string := lexeme[1 : len(lexeme)-1]

	c.emitConstant(value.String(string))
}

func (c *Compiler) literal(canAssign bool) {
	switch c.p.Previous().Type() {
	case parser.False:
		c.emitOpCode(False)
	case parser.True:
		c.emitOpCode(True)
	case parser.Nil:
		c.emitOpCode(Nil)
	default:
		panic("unreachable")
	}
}

func (c *Compiler) emitByte(byte uint8) {
	c.chunk.pushCode(byte)
}

func (c *Compiler) emitShort(short uint16) {
	c.chunk.pushCode(uint8((short >> 8) & 0xff))
	c.chunk.pushCode(uint8(short & 0xff))
}

func (c *Compiler) emitOpCode(opCode OpCode) {
	c.chunk.pushCode(uint8(opCode))
}

func (c *Compiler) emitConstant(value value.Value) {
	constant := c.chunk.pushConstant(value)

	c.emitOpCode(Constant)
	c.emitShort(constant)
}

func (c *Compiler) emitReturn() {
	c.emitOpCode(Nil)
	c.emitOpCode(Return)
}

func (c *Compiler) consumeNewlines() {
	for c.p.Current().Type() == parser.Newline {
		c.p.SetCurrent(c.p.NextToken())
	}
}

func (c *Compiler) skipNewlines() {
	switch c.p.Previous().Type() {
	case parser.Newline, parser.LeftBrace, parser.RightBrace, parser.Semicolon, parser.Dot:
		c.consumeNewlines()
	}
}

func (c *Compiler) expectNewlineOrSemicolon() {
	// TODO : Improve consuming of newlines
	// If previous token is RightBrace then all newlines were already consumed
	if c.p.Previous().Type() == parser.RightBrace {
		return
	}

	// If current token is RightBrace then we don't need newline nor semicolon
	if c.p.Current().Type() == parser.RightBrace {
		return
	}

	// If we are at the end of the file then we don't need newline nor semicolon
	if c.p.Previous().Type() == parser.Eof || c.p.Current().Type() == parser.Eof {
		return
	}

	if !c.match(parser.Semicolon) {
		c.consume(parser.Newline, "Expect newline or ';'.")
	}
}

func (c *Compiler) advance() {
	c.p.SetPrevious(c.p.Current())

	for true {
		c.p.SetCurrent(c.p.NextToken())
		if c.p.Current().Type() != parser.Error {
			break
		}

		c.errorAtCurrent(c.p.Current().Lexeme())
	}

	c.skipNewlines()
}

// Checks whether next token is of the given type.
// Returns true if so, otherwise returns false.
func (c *Compiler) check(tokenType parser.TokenType) bool {
	if tokenType == parser.Newline && c.p.Previous().Type() == parser.RightBrace {
		return true
	}

	return c.p.Current().Type() == tokenType
}

func (c *Compiler) consume(tokenType parser.TokenType, message string) {
	if c.check(tokenType) {
		c.advance()

		return
	}

	c.errorAtCurrent(message)
}

// Checks whether next token is of the given type.
// If yes, consumes it and returns true, otherwise it does not consume any tokens and return false.
func (c *Compiler) match(tokenType parser.TokenType) bool {
	if !c.check(tokenType) {
		return false
	}

	c.advance()

	return true
}

func (c *Compiler) error(message string) {
	c.errorAt(c.p.Previous(), message)
}

func (c *Compiler) errorAtCurrent(message string) {
	c.errorAt(c.p.Current(), message)
}

func (c *Compiler) errorAt(token parser.Token, message string) {
	if c.panicMode {
		return
	}

	c.panicMode = true

	_, _ = fmt.Fprintf(os.Stderr, "[line %d] Error", token.Line())

	switch token.Type() {
	case parser.Eof:
		_, _ = fmt.Fprintf(os.Stderr, " at end")
	case parser.Newline:
		_, _ = fmt.Fprintf(os.Stderr, " at newline")
	default:
		_, _ = fmt.Fprintf(os.Stderr, " at '%s'", token.Lexeme())
	}

	_, _ = fmt.Fprintf(os.Stderr, ": %s\n", message)

	c.hadError = true
}
