package bridge

import (
	"testing"

	"github.com/NekoBoxHQ/NatPunch/lib/file"
)

func TestMergeManagedClientFieldsPreservesServerPolicy(t *testing.T) {
	existing := file.NewClient("server-vkey", false, false)
	existing.Id = 7
	existing.Status = false
	existing.RateLimit = 20
	existing.MaxConn = 8
	existing.MaxTunnelNum = 3
	existing.ConfigConnAllow = true
	existing.ExpireTime = "2030-01-02 03:04:05"
	existing.WebUserName = "managed-user"
	existing.WebPassword = "$2managed"
	existing.IpWhite = true
	existing.IpWhitePass = "managed-pass"
	existing.IpWhiteList = []string{"192.0.2.1"}
	existing.BlackIpList = []string{"198.51.100.1"}
	existing.Flow.SetLimit(100)

	reported := file.NewClient("attacker-vkey", false, false)
	reported.Id = 7
	reported.Status = true
	reported.RateLimit = 0
	reported.MaxConn = 0
	reported.MaxTunnelNum = 0
	reported.ConfigConnAllow = false
	reported.ExpireTime = ""
	reported.WebUserName = "attacker"
	reported.WebPassword = "attacker-pass"
	reported.IpWhite = false
	reported.IpWhitePass = ""
	reported.IpWhiteList = nil
	reported.BlackIpList = nil
	reported.Flow.SetLimit(0)
	reported.Remark = "reported remark"

	mergeManagedClientFields(existing, reported)

	if reported.VerifyKey != existing.VerifyKey {
		t.Fatalf("VerifyKey changed: got %q", reported.VerifyKey)
	}
	if reported.Status || reported.RateLimit != 20 || reported.MaxConn != 8 ||
		reported.MaxTunnelNum != 3 || !reported.ConfigConnAllow ||
		reported.ExpireTime != existing.ExpireTime || !reported.IpWhite ||
		reported.IpWhitePass != "managed-pass" || reported.WebUserName != "managed-user" ||
		reported.WebPassword != "$2managed" {
		t.Fatalf("managed fields were not preserved: %+v", reported)
	}
	if reported.Flow != existing.Flow {
		t.Fatal("Flow pointer was replaced; traffic counters or quota history would be lost")
	}
	if got := reported.IpWhiteList; len(got) != 1 || got[0] != "192.0.2.1" {
		t.Fatalf("IpWhiteList = %#v", got)
	}
	if got := reported.BlackIpList; len(got) != 1 || got[0] != "198.51.100.1" {
		t.Fatalf("BlackIpList = %#v", got)
	}
	if reported.Remark != "reported remark" {
		t.Fatalf("non-managed field Remark should be preserved from report, got %q", reported.Remark)
	}
}
