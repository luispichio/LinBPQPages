package bpq

// Field describe un campo del registro empaquetado DIRMES.SYS de LinBPQ.
type Field struct {
	Offset int
	Size   int
}

// Layout describe struct MsgInfo en disco. bpqmail.h mantiene esta estructura
// bajo #pragma pack(1), por lo que los offsets no incluyen padding de ABI.
type Layout struct {
	RecordSize                                    int
	Type, Status, Number, Length, LegacyReceived  Field
	BBSFrom, Via, From, To, BID, Title            Field
	NNTPNumber, B2Flags, LegacyCreated            Field
	LegacyChanged, ForwardTo, Forwarded           Field
	EmailFrom, Locked, Deferred, UTF8             Field
	DateReceived, DateCreated, DateChanged, Spare Field
}

// CurrentLayout coincide con struct MsgInfo de 308 bytes del fuente LinBPQ y
// fue verificado contra el DIRMES.SYS instalado.
func CurrentLayout() Layout {
	return Layout{
		RecordSize:     308,
		Type:           Field{Offset: 0, Size: 1},
		Status:         Field{Offset: 1, Size: 1},
		Number:         Field{Offset: 2, Size: 4},
		Length:         Field{Offset: 6, Size: 4},
		LegacyReceived: Field{Offset: 10, Size: 4},
		BBSFrom:        Field{Offset: 14, Size: 7},
		Via:            Field{Offset: 21, Size: 41},
		From:           Field{Offset: 62, Size: 7},
		To:             Field{Offset: 69, Size: 7},
		BID:            Field{Offset: 76, Size: 13},
		Title:          Field{Offset: 89, Size: 61},
		NNTPNumber:     Field{Offset: 150, Size: 4},
		B2Flags:        Field{Offset: 154, Size: 1},
		LegacyCreated:  Field{Offset: 155, Size: 4},
		LegacyChanged:  Field{Offset: 159, Size: 4},
		ForwardTo:      Field{Offset: 163, Size: 20},
		Forwarded:      Field{Offset: 183, Size: 20},
		EmailFrom:      Field{Offset: 203, Size: 41},
		Locked:         Field{Offset: 244, Size: 1},
		Deferred:       Field{Offset: 245, Size: 1},
		UTF8:           Field{Offset: 246, Size: 1},
		DateReceived:   Field{Offset: 247, Size: 8},
		DateCreated:    Field{Offset: 255, Size: 8},
		DateChanged:    Field{Offset: 263, Size: 8},
		Spare:          Field{Offset: 271, Size: 37},
	}
}
