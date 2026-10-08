package ipcepoch

import "testing"

func TestBootIDBounds(t *testing.T) {
	valid := "d4f2766e-fa8d-4f0c-baee-668a6cf28f6d"
	if !validBootID(valid) {
		t.Fatal("valid boot UUID refused")
	}
	for _, s := range []string{"", valid + "\n", valid[:35], "D4F2766E-FA8D-4F0C-BAEE-668A6CF28F6D", "00000000-0000-0000-0000-000000000000", "d4f2766e_fa8d-4f0c-baee-668a6cf28f6d", "z4f2766e-fa8d-4f0c-baee-668a6cf28f6d"} {
		if validBootID(s) {
			t.Fatal("invalid boot UUID accepted", s)
		}
	}
}
