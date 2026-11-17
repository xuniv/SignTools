//go:build (amd64 || arm64) && (linux || darwin || windows)

package store

import (
	"context"
	"strings"
	"testing"

	"SignTools/src/library"
	"github.com/majd/ipatool/v2/pkg/appstore"
	"howett.net/plist"
)

func repairFixtureIPA(t *testing.T, appleID string) []byte {
	t.Helper()
	metadata, err := plist.Marshal(map[string]any{"itemId": uint64(42), "softwareVersionExternalIdentifier": uint64(901), "apple-id": appleID}, plist.BinaryFormat)
	if err != nil {
		t.Fatal(err)
	}
	return addIPAEntries(t, fixtureManifestIPA(t, nil), map[string][]byte{"iTunesMetadata.plist": metadata, "Payload/Synthetic.app/SC_Info/Synthetic.supf": []byte("original-fairplay-payload")})
}
func TestLicenseRepairMatchesAccountAndExactOriginalPayload(t *testing.T) {
	service, mock := fixtureService(t)
	source := repairFixtureIPA(t, "ONE@example.test")
	original, err := library.Import(strings.NewReader(string(source)), "broken.ipa", "ipatool", "")
	if err != nil {
		t.Fatal(err)
	}
	mock.ipa = source
	mock.sinfs = []appstore.Sinf{{ID: 42, Data: []byte("licensed-sinf-returned-by-Apple")}}
	other := loginFixture(t, service, "other@example.test")
	matching := loginFixture(t, service, "one@example.test")
	result, err := service.Repair(context.Background(), RepairRequest{ClientID: browserID, ArtifactID: original.ID, Sessions: []string{"invalid-sealed-session", other.Session, matching.Session}}, Progress{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Artifact.OriginalID != original.ID || result.Artifact.ID == original.ID || result.Account.ID != matching.Account.ID || result.Session == matching.Session {
		t.Fatalf("repair did not create a verified immutable child and update account: %+v", result.Artifact)
	}
	before, err := packageFingerprint(context.Background(), original.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	after, err := packageFingerprint(context.Background(), result.Artifact.ID, false)
	if err != nil || !sameRepairPayload(before, after) {
		t.Fatal("repair changed executable or FairPlay payload")
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if strings.Join(mock.versions, ",") != "901" || mock.calls["/purchase"] != 0 {
		t.Fatal("repair fetched a different version or acquired a license")
	}
}
func TestRepairRefusesAccountMismatchMissingExactMetadataAndChangedPayload(t *testing.T) {
	for _, scenario := range []string{"account-mismatch", "missing-version", "changed-executable", "changed-supf", "changed-version", "upload-source"} {
		t.Run(scenario, func(t *testing.T) {
			service, mock := fixtureService(t)
			source := repairFixtureIPA(t, "one@example.test")
			download := source
			owner := "one@example.test"
			origin := "ipatool"
			switch scenario {
			case "account-mismatch":
				owner = "other@example.test"
			case "missing-version":
				metadata, _ := plist.Marshal(map[string]any{"itemId": 42, "apple-id": "one@example.test"}, plist.XMLFormat)
				source = addIPAEntries(t, source, map[string][]byte{"iTunesMetadata.plist": metadata})
			case "changed-executable":
				download = addIPAEntries(t, download, map[string][]byte{"Payload/Synthetic.app/Synthetic": append(append([]byte(nil), fixtureExecutable()...), 1)})
			case "changed-supf":
				download = addIPAEntries(t, download, map[string][]byte{"Payload/Synthetic.app/SC_Info/Synthetic.supf": []byte("changed-fairplay-payload")})
			case "changed-version":
				metadata, _ := plist.Marshal(map[string]any{"CFBundleIdentifier": "com.example.synthetic", "CFBundleDisplayName": "Synthetic Store App", "CFBundleShortVersionString": "902", "CFBundleVersion": "902", "CFBundleExecutable": "Synthetic"}, plist.XMLFormat)
				download = addIPAEntries(t, download, map[string][]byte{"Payload/Synthetic.app/Info.plist": metadata})
			case "upload-source":
				origin = "upload"
			}
			artifact, err := library.Import(strings.NewReader(string(source)), "broken.ipa", origin, "")
			if err != nil {
				t.Fatal(err)
			}
			mock.ipa = download
			mock.sinfs = []appstore.Sinf{{ID: 42, Data: []byte("returned-license")}}
			login := loginFixture(t, service, owner)
			_, err = service.Repair(context.Background(), RepairRequest{ClientID: browserID, ArtifactID: artifact.ID, Sessions: []string{login.Session}}, Progress{})
			if err == nil {
				t.Fatal("unsafe automatic license repair succeeded")
			}
			items, listErr := library.List()
			if listErr != nil || len(items) != 1 || items[0].ID != artifact.ID {
				t.Fatalf("refused repair left an orphaned replacement: %+v %v", items, listErr)
			}
			mock.mu.Lock()
			downloads := mock.calls["/ipa"]
			mock.mu.Unlock()
			if (scenario == "account-mismatch" || scenario == "missing-version" || scenario == "upload-source") && downloads != 0 {
				t.Fatal("unqualified repair contacted the download endpoint")
			}
		})
	}
}
func fixtureExecutable() []byte {
	// Same valid encrypted Mach-O used by fixtureIPA; no executable code runs.
	data := make([]byte, 56)
	copy(data, []byte{0xcf, 0xfa, 0xed, 0xfe})
	data[7] = 1
	data[4] = 0x0c
	data[12] = 2
	data[16] = 1
	data[20] = 24
	data[32] = 0x2c
	data[36] = 24
	data[48] = 1
	return data
}
