package viscaoverip

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

func TestMakeCommand(t *testing.T) {
	type testCase struct {
		name    string
		command string
		seqNum  int
		wantStr string
	}
	tests := []testCase{
		{
			"Pantilt: Home",
			"06 04",
			1234, // 04D2
			"0100 0005 000004D2 8101 06 04 FF",
		},
		{
			"Pantilt: Up Max Speed",
			"06 01 18 14 03 01",
			1865, // 0749
			"0100 0009 00000749 81 01 06 01 18 14 03 01 FF",
		},
		{
			"Cam Zoom: Tele (Variable)",
			"04 07 22", // 81 01 04 07 2p FF: p=0(low)~7(high)
			2489321654, // 946008B6
			"0100 0006 946008B6 81 01 04 07 22 FF",
		},
	}

	for _, tc := range tests {

		wantStr := strings.ReplaceAll(tc.wantStr, " ", "")
		want, err := hex.DecodeString(wantStr)
		if err != nil {
			t.Fatal(err)
		}

		message, err := MakeCommand(tc.command, tc.seqNum)

		if !bytes.Equal(message, want) || err != nil {
			t.Errorf("\n%s,\nMakeCommand(%s) = %#v, %v,\nwant %#v, nil", tc.name, tc.command, message, err, want)
		}
	}
}
