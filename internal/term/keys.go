package term

import (
	"strconv"

	uv "github.com/charmbracelet/ultraviolet"
	tvt "tuios.dev/vt"
)

// EncodeKey returns the bytes a key press sends to a guest with the given modes, or nil.
func EncodeKey(k uv.KeyPressEvent, m KeyModes) []byte {
	if m.KittyFlags != 0 || m.ModifyOtherKeys != 0 {
		if s := tvt.EncodePaneKey(k, m.KittyFlags, m.ModifyOtherKeys); s != "" {
			return []byte(s)
		}
	}

	return encodeLegacy(k, m)
}

// EncodePaste sanitises text and wraps it in bracketed-paste markers when bracketed is true.
func EncodePaste(text string, bracketed bool) []byte {
	text = tvt.SanitizePaste(text)
	if !bracketed {
		return []byte(text)
	}

	return []byte("\x1b[200~" + text + "\x1b[201~")
}

// keyForm is how a special key is written in the legacy encoding.
type keyForm int

const (
	formCSI   keyForm = iota // CSI final, SS3 final with AppCursor
	formSS3                  // SS3 final unmodified (F1-F4)
	formTilde                // CSI num ~
)

type specialKey struct {
	form  keyForm
	final byte // formCSI and formSS3
	num   int  // formTilde
}

// The legacy encoding follows xterm's ctlseqs PC-style function keys.
var specialKeys = map[rune]specialKey{
	uv.KeyUp:    {form: formCSI, final: 'A'},
	uv.KeyDown:  {form: formCSI, final: 'B'},
	uv.KeyRight: {form: formCSI, final: 'C'},
	uv.KeyLeft:  {form: formCSI, final: 'D'},
	uv.KeyBegin: {form: formCSI, final: 'E'},
	uv.KeyEnd:   {form: formCSI, final: 'F'},
	uv.KeyHome:  {form: formCSI, final: 'H'},

	uv.KeyKpUp:    {form: formCSI, final: 'A'},
	uv.KeyKpDown:  {form: formCSI, final: 'B'},
	uv.KeyKpRight: {form: formCSI, final: 'C'},
	uv.KeyKpLeft:  {form: formCSI, final: 'D'},
	uv.KeyKpBegin: {form: formCSI, final: 'E'},
	uv.KeyKpEnd:   {form: formCSI, final: 'F'},
	uv.KeyKpHome:  {form: formCSI, final: 'H'},

	uv.KeyF1: {form: formSS3, final: 'P'},
	uv.KeyF2: {form: formSS3, final: 'Q'},
	uv.KeyF3: {form: formSS3, final: 'R'},
	uv.KeyF4: {form: formSS3, final: 'S'},

	uv.KeyInsert:   {form: formTilde, num: 2},
	uv.KeyDelete:   {form: formTilde, num: 3},
	uv.KeyPgUp:     {form: formTilde, num: 5},
	uv.KeyPgDown:   {form: formTilde, num: 6},
	uv.KeyKpInsert: {form: formTilde, num: 2},
	uv.KeyKpDelete: {form: formTilde, num: 3},
	uv.KeyKpPgUp:   {form: formTilde, num: 5},
	uv.KeyKpPgDown: {form: formTilde, num: 6},
	uv.KeyF5:       {form: formTilde, num: 15},
	uv.KeyF6:       {form: formTilde, num: 17},
	uv.KeyF7:       {form: formTilde, num: 18},
	uv.KeyF8:       {form: formTilde, num: 19},
	uv.KeyF9:       {form: formTilde, num: 20},
	uv.KeyF10:      {form: formTilde, num: 21},
	uv.KeyF11:      {form: formTilde, num: 23},
	uv.KeyF12:      {form: formTilde, num: 24},
}

// keypadKeys maps keypad keys to their plain character and SS3 final (AppKeypad).
var keypadKeys = map[rune]struct{ plain, app byte }{
	uv.KeyKp0:        {'0', 'p'},
	uv.KeyKp1:        {'1', 'q'},
	uv.KeyKp2:        {'2', 'r'},
	uv.KeyKp3:        {'3', 's'},
	uv.KeyKp4:        {'4', 't'},
	uv.KeyKp5:        {'5', 'u'},
	uv.KeyKp6:        {'6', 'v'},
	uv.KeyKp7:        {'7', 'w'},
	uv.KeyKp8:        {'8', 'x'},
	uv.KeyKp9:        {'9', 'y'},
	uv.KeyKpEnter:    {'\r', 'M'},
	uv.KeyKpEqual:    {'=', 'X'},
	uv.KeyKpMultiply: {'*', 'j'},
	uv.KeyKpPlus:     {'+', 'k'},
	uv.KeyKpComma:    {',', 'l'},
	uv.KeyKpMinus:    {'-', 'm'},
	uv.KeyKpDecimal:  {'.', 'n'},
	uv.KeyKpDivide:   {'/', 'o'},
}

func encodeLegacy(k uv.KeyPressEvent, m KeyModes) []byte {
	if sk, ok := specialKeys[k.Code]; ok {
		return encodeSpecial(sk, k.Mod, m)
	}

	alt := k.Mod.Contains(uv.ModAlt)

	var base []byte

	switch kp, isKeypad := keypadKeys[k.Code]; {
	case isKeypad && m.AppKeypad:
		base = []byte{0x1b, 'O', kp.app}
	case isKeypad:
		base = []byte{kp.plain}
	default:
		base = encodeBase(k)
	}

	if base == nil {
		return nil
	}

	if alt {
		return append([]byte{0x1b}, base...)
	}

	return base
}

// encodeSpecial writes a cursor, function or editing key. Any modifier
// switches to the CSI form with the modifier parameter.
func encodeSpecial(sk specialKey, mod uv.KeyMod, m KeyModes) []byte {
	p := modParam(mod)

	switch sk.form {
	case formTilde:
		if p == 1 {
			return appendCSI(nil, sk.num, 0, '~')
		}

		return appendCSI(nil, sk.num, p, '~')
	case formSS3:
		if p == 1 {
			return []byte{0x1b, 'O', sk.final}
		}
	case formCSI:
		if p == 1 && m.AppCursor {
			return []byte{0x1b, 'O', sk.final}
		}

		if p == 1 {
			return []byte{0x1b, '[', sk.final}
		}
	}

	return appendCSI(nil, 1, p, sk.final)
}

// modParam is xterm's modifier parameter: 1 plus shift, alt, ctrl and meta bits.
func modParam(mod uv.KeyMod) int {
	p := 1

	if mod.Contains(uv.ModShift) {
		p++
	}

	if mod.Contains(uv.ModAlt) {
		p += 2
	}

	if mod.Contains(uv.ModCtrl) {
		p += 4
	}

	if mod.Contains(uv.ModMeta) {
		p += 8
	}

	return p
}

// appendCSI appends ESC [ num ; mod final. A zero mod omits the parameter.
func appendCSI(b []byte, num, mod int, final byte) []byte {
	b = append(b, 0x1b, '[')
	b = strconv.AppendInt(b, int64(num), 10)

	if mod != 0 {
		b = append(b, ';')
		b = strconv.AppendInt(b, int64(mod), 10)
	}

	return append(b, final)
}

// encodeBase is what a text, control or whitespace key sends without alt;
// the caller adds the ESC prefix for alt. nil means the key has no encoding.
func encodeBase(k uv.KeyPressEvent) []byte {
	ctrl := k.Mod.Contains(uv.ModCtrl)

	switch k.Code {
	case uv.KeyEnter:
		return []byte{'\r'}
	case uv.KeyTab:
		if k.Mod.Contains(uv.ModShift) {
			return []byte("\x1b[Z")
		}

		return []byte{'\t'}
	case uv.KeyBackspace:
		if ctrl {
			return []byte{0x08}
		}

		return []byte{0x7f}
	case uv.KeyEscape:
		return []byte{0x1b}
	}

	// Kitty-protocol terminals send a modified key as its unshifted code with
	// no text; the shifted character arrives as ShiftedCode.
	char := k.Code
	if k.Text == "" && k.Mod.Contains(uv.ModShift) && k.ShiftedCode != 0 {
		char = k.ShiftedCode
	}

	if ctrl {
		if c, ok := ctrlByte(char); ok {
			return []byte{c}
		}
	}

	if k.Text != "" && !ctrl {
		return []byte(k.Text)
	}

	// A bare printable ASCII code (ctrl+1) sends the character; other codes
	// without text have no encoding.
	if char >= 0x20 && char < 0x7f {
		return []byte{byte(char)}
	}

	return nil
}

// ctrlByte maps ctrl+letter, ctrl+space, ctrl+@[\]^_? and the xterm
// ctrl+/ and ctrl+2..8 aliases to their C0 byte.
func ctrlByte(c rune) (byte, bool) {
	switch {
	case c >= 'a' && c <= 'z':
		return byte(c - 'a' + 1), true
	case c >= 'A' && c <= 'Z':
		return byte(c - 'A' + 1), true
	case c == ' ' || c == '@':
		return 0, true
	case c >= '[' && c <= '_':
		return byte(c - '@'), true
	case c >= '{' && c <= '~':
		// The shifted forms of [ \ ] ^; xterm clears the high bits.
		return byte(c - '`'), true
	case c == '?', c == '8':
		return 0x7f, true
	case c == '/', c == '7':
		return 0x1f, true
	case c == '2':
		return 0, true
	case c >= '3' && c <= '6':
		return byte(c-'3') + 0x1b, true
	}

	return 0, false
}
