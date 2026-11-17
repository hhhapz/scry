package proto

// Android input constants (android/input.h, android/keycodes.h), the subset
// a desktop client sends.

// Key event actions.
const (
	KeyDown byte = 0
	KeyUp   byte = 1
)

// Motion event actions.
const (
	MotionDown      byte = 0
	MotionUp        byte = 1
	MotionMove      byte = 2
	MotionCancel    byte = 3
	MotionHoverMove byte = 7
)

// Motion event buttons.
const (
	ButtonPrimary   uint32 = 1 << 0
	ButtonSecondary uint32 = 1 << 1
	ButtonTertiary  uint32 = 1 << 2
	ButtonBack      uint32 = 1 << 3
	ButtonForward   uint32 = 1 << 4
)

// Meta state flags.
const (
	MetaShiftOn     uint32 = 0x01
	MetaAltOn       uint32 = 0x02
	MetaShiftLeftOn uint32 = 0x40
	MetaCtrlOn      uint32 = 0x1000
	MetaMetaOn      uint32 = 0x10000
)

// Key codes.
const (
	KeycodeHome         uint32 = 3
	KeycodeBack         uint32 = 4
	Keycode0            uint32 = 7 // 0..9 are consecutive
	KeycodeDpadUp       uint32 = 19
	KeycodeDpadDown     uint32 = 20
	KeycodeDpadLeft     uint32 = 21
	KeycodeDpadRight    uint32 = 22
	KeycodeDpadCenter   uint32 = 23
	KeycodeVolumeUp     uint32 = 24
	KeycodeVolumeDown   uint32 = 25
	KeycodePower        uint32 = 26
	KeycodeA            uint32 = 29 // A..Z are consecutive
	KeycodeComma        uint32 = 55
	KeycodePeriod       uint32 = 56
	KeycodeAltLeft      uint32 = 57
	KeycodeAltRight     uint32 = 58
	KeycodeShiftLeft    uint32 = 59
	KeycodeShiftRight   uint32 = 60
	KeycodeTab          uint32 = 61
	KeycodeSpace        uint32 = 62
	KeycodeEnter        uint32 = 66
	KeycodeDel          uint32 = 67 // backspace
	KeycodeGrave        uint32 = 68
	KeycodeMinus        uint32 = 69
	KeycodeEquals       uint32 = 70
	KeycodeLeftBracket  uint32 = 71
	KeycodeRightBracket uint32 = 72
	KeycodeBackslash    uint32 = 73
	KeycodeSemicolon    uint32 = 74
	KeycodeApostrophe   uint32 = 75
	KeycodeSlash        uint32 = 76
	KeycodeMenu         uint32 = 82
	KeycodePageUp       uint32 = 92
	KeycodePageDown     uint32 = 93
	KeycodeEscape       uint32 = 111
	KeycodeForwardDel   uint32 = 112
	KeycodeCtrlLeft     uint32 = 113
	KeycodeCtrlRight    uint32 = 114
	KeycodeMetaLeft     uint32 = 117
	KeycodeMetaRight    uint32 = 118
	KeycodeMoveHome     uint32 = 122
	KeycodeMoveEnd      uint32 = 123
	KeycodeF1           uint32 = 131 // F1..F12 are consecutive
	KeycodeNumpadEnter  uint32 = 160
	KeycodeVolumeMute   uint32 = 164
	KeycodeAppSwitch    uint32 = 187
	KeycodeSleep        uint32 = 223
	KeycodeWakeup       uint32 = 224
	KeycodeCut          uint32 = 277
	KeycodeCopy         uint32 = 278
	KeycodePaste        uint32 = 279
)
