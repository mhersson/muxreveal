package theme

import (
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2"
)

// syntaxEntry styles one token type with palette roles; an empty Hex leaves
// that colour unset.
type syntaxEntry struct {
	tt     chroma.TokenType
	attrs  string
	fg, bg Hex
}

func (e syntaxEntry) style() string {
	parts := make([]string, 0, 3)
	if e.attrs != "" {
		parts = append(parts, e.attrs)
	}
	if e.bg != "" {
		parts = append(parts, "bg:"+string(e.bg))
	}
	if e.fg != "" {
		parts = append(parts, string(e.fg))
	}

	return strings.Join(parts, " ")
}

// syntaxEntries is Chroma's bundled Catppuccin style (MIT, see THIRD_PARTY.md)
// with each colour replaced by its palette role; all four flavors share it.
func syntaxEntries(p Palette) []syntaxEntry {
	return []syntaxEntry{
		{chroma.Background, "", p.Text, p.Base},
		{chroma.CodeLine, "", p.Text, ""},
		{chroma.Error, "", p.Red, ""},
		{chroma.Other, "", p.Text, ""},
		{chroma.LineTableTD, "", "", ""},
		{chroma.LineTable, "", "", ""},
		{chroma.LineHighlight, "", "", p.Surface1},
		{chroma.LineNumbersTable, "", p.Overlay1, ""},
		{chroma.LineNumbers, "", p.Overlay1, ""},
		{chroma.Keyword, "", p.Mauve, ""},
		{chroma.KeywordReserved, "", p.Mauve, ""},
		{chroma.KeywordPseudo, "", p.Mauve, ""},
		{chroma.KeywordConstant, "", p.Peach, ""},
		{chroma.KeywordDeclaration, "", p.Red, ""},
		{chroma.KeywordNamespace, "", p.Teal, ""},
		{chroma.KeywordType, "", p.Red, ""},
		{chroma.Name, "", p.Text, ""},
		{chroma.NameClass, "", p.Yellow, ""},
		{chroma.NameConstant, "", p.Yellow, ""},
		{chroma.NameDecorator, "bold", p.Blue, ""},
		{chroma.NameEntity, "", p.Teal, ""},
		{chroma.NameException, "", p.Peach, ""},
		{chroma.NameFunction, "", p.Blue, ""},
		{chroma.NameFunctionMagic, "", p.Blue, ""},
		{chroma.NameLabel, "", p.Sky, ""},
		{chroma.NameNamespace, "", p.Peach, ""},
		{chroma.NameProperty, "", p.Peach, ""},
		{chroma.NameTag, "", p.Mauve, ""},
		{chroma.NameVariable, "", p.Rosewater, ""},
		{chroma.NameVariableClass, "", p.Rosewater, ""},
		{chroma.NameVariableGlobal, "", p.Rosewater, ""},
		{chroma.NameVariableInstance, "", p.Rosewater, ""},
		{chroma.NameVariableMagic, "", p.Rosewater, ""},
		{chroma.NameAttribute, "", p.Blue, ""},
		{chroma.NameBuiltin, "", p.Sky, ""},
		{chroma.NameBuiltinPseudo, "", p.Sky, ""},
		{chroma.NameOther, "", p.Text, ""},
		{chroma.Literal, "", p.Text, ""},
		{chroma.LiteralDate, "", p.Text, ""},
		{chroma.LiteralString, "", p.Green, ""},
		{chroma.LiteralStringChar, "", p.Green, ""},
		{chroma.LiteralStringSingle, "", p.Green, ""},
		{chroma.LiteralStringDouble, "", p.Green, ""},
		{chroma.LiteralStringBacktick, "", p.Green, ""},
		{chroma.LiteralStringOther, "", p.Green, ""},
		{chroma.LiteralStringSymbol, "", p.Green, ""},
		{chroma.LiteralStringInterpol, "", p.Green, ""},
		{chroma.LiteralStringAffix, "", p.Red, ""},
		{chroma.LiteralStringDelimiter, "", p.Blue, ""},
		{chroma.LiteralStringEscape, "", p.Blue, ""},
		{chroma.LiteralStringRegex, "", p.Teal, ""},
		{chroma.LiteralStringDoc, "", p.Overlay0, ""},
		{chroma.LiteralStringHeredoc, "", p.Overlay0, ""},
		{chroma.LiteralNumber, "", p.Peach, ""},
		{chroma.LiteralNumberBin, "", p.Peach, ""},
		{chroma.LiteralNumberHex, "", p.Peach, ""},
		{chroma.LiteralNumberInteger, "", p.Peach, ""},
		{chroma.LiteralNumberFloat, "", p.Peach, ""},
		{chroma.LiteralNumberIntegerLong, "", p.Peach, ""},
		{chroma.LiteralNumberOct, "", p.Peach, ""},
		{chroma.Operator, "bold", p.Sky, ""},
		{chroma.OperatorWord, "bold", p.Sky, ""},
		{chroma.Comment, "italic", p.Overlay0, ""},
		{chroma.CommentSingle, "italic", p.Overlay0, ""},
		{chroma.CommentMultiline, "italic", p.Overlay0, ""},
		{chroma.CommentSpecial, "italic", p.Overlay0, ""},
		{chroma.CommentHashbang, "italic", p.Surface2, ""},
		{chroma.CommentPreproc, "italic", p.Overlay0, ""},
		{chroma.CommentPreprocFile, "bold", p.Overlay0, ""},
		{chroma.Generic, "", p.Text, ""},
		{chroma.GenericInserted, "", p.Green, p.Surface0},
		{chroma.GenericDeleted, "", p.Red, p.Surface0},
		{chroma.GenericEmph, "italic", p.Text, ""},
		{chroma.GenericStrong, "bold", p.Text, ""},
		{chroma.GenericUnderline, "underline", p.Text, ""},
		{chroma.GenericHeading, "bold", p.Peach, ""},
		{chroma.GenericSubheading, "bold", p.Peach, ""},
		{chroma.GenericOutput, "", p.Text, ""},
		{chroma.GenericPrompt, "", p.Text, ""},
		{chroma.GenericError, "", p.Red, ""},
		{chroma.GenericTraceback, "", p.Red, ""},
	}
}

// Syntax returns a Chroma style, named after the theme, coloured from its palette.
func (t Theme) Syntax() (*chroma.Style, error) {
	b := chroma.NewStyleBuilder(t.Name)
	for _, e := range syntaxEntries(t.Palette) {
		b.Add(e.tt, e.style())
	}
	style, err := b.Build()
	if err != nil {
		return nil, fmt.Errorf("build syntax style %s: %w", t.Name, err)
	}

	return style, nil
}
