//go:build darwin || linux

package store

import (
	"errors"
	"testing"
	"time"
)

func TestSNMPStateCapacityRollback(t *testing.T) {
	cases := []struct {
		table  string
		limit  int
		insert string
	}{
		{"snmp_engines", 1024, "INSERT INTO snmp_engines SELECT randomblob(8),2,1000,1000,1000,1000 FROM n"},
		{"snmp_owners", 4096, "INSERT INTO snmp_owners SELECT x'8000000102030405',printf('user%d',i),'device','backend',x'00',x'8000000102030405','' FROM n"},
		{"snmp_seen", 16384, "INSERT INTO snmp_seen SELECT printf('%064x',i),x'8000000102030406','fixture',2,1000,zeroblob(32) FROM n"},
	}
	for _, tc := range cases {
		t.Run(tc.table, func(t *testing.T) {
			c, _, _ := fixture(t)
			factory := snmpFactory(t, c, "fixture", "device")
			s, err := Open(t.Context(), c)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if _, err := s.db.ExecContext(t.Context(), "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<?) "+tc.insert, tc.limit); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.AdmitSNMP(t.Context(), factory(2, 1000, 1, "body"), time.Unix(1000, 0)); !errors.Is(err, ErrCapacity) {
				t.Fatal("state bound bypassed", err)
			}
			assertSNMPCount(t, s, tc.table, tc.limit)
			assertSNMPCount(t, s, "jobs", 0)
			for _, table := range []string{"snmp_engines", "snmp_owners", "snmp_seen"} {
				if table != tc.table {
					assertSNMPCount(t, s, table, 0)
				}
			}
		})
	}
}
