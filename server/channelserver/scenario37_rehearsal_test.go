package channelserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"erupe-ce/common/byteframe"
	cfg "erupe-ce/config"
	"erupe-ce/network/mhfpacket"
)

// This external-fixture test checks the public counter/file wire path only.
// It neither registers the NPC nor runs a chapter, grants an item, or saves.
func TestScenario37OriginalSupplyWireRehearsal(t *testing.T) {
	dir := os.Getenv("ERUPE_SCENARIO37_REHEARSAL_DIR")
	if dir == "" {
		t.Skip("explicit captured public S37 files and roster required")
	}
	binPath := os.Getenv("ERUPE_SCENARIO37_REHEARSAL_BIN")
	if binPath == "" {
		t.Fatal("explicit S37 binary input required")
	}
	var input struct {
		Files []struct {
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
			Bytes  int    `json:"bytes"`
		} `json:"files"`
		Counters []struct {
			MainID   uint32 `json:"scenario_id"`
			Category uint8  `json:"category_id"`
		} `json:"counter_rows"`
	}
	rehearsalJSON(t, filepath.Join(dir, "scenario37-file-capture.json"), &input)
	if len(input.Files) != 12 || len(input.Counters) != 171 {
		t.Fatal("unexpected capture scope")
	}
	rows := make([]Scenario, 0, len(input.Counters)+1)
	seen := make(map[Scenario]bool)
	for _, row := range input.Counters {
		scenario := Scenario{MainID: row.MainID, CategoryID: row.Category}
		if seen[scenario] || scenario == (Scenario{MainID: 37, CategoryID: 1}) {
			t.Fatal("capture is not the unique pre-registration roster")
		}
		seen[scenario] = true
		rows = append(rows, scenario)
	}
	for _, test := range []struct {
		addOriginal, enabled bool
		want                 int
	}{{false, true, 171}, {true, true, 172}, {true, false, 128}} {
		server := createMockServer()
		server.erupeConfig.RealClientMode = cfg.ZZ
		server.erupeConfig.QuestDelivery.Enabled = test.enabled
		server.erupeConfig.BinPath = binPath
		fixtureRows := append([]Scenario(nil), rows...)
		if test.addOriginal {
			fixtureRows = append(fixtureRows, Scenario{MainID: 37, CategoryID: 1})
		}
		before := append([]Scenario(nil), fixtureRows...)
		server.scenarioRepo = &mockScenarioRepo{scenarios: fixtureRows}
		session := createMockSession(1, server)
		handleMsgMhfInfoScenarioCounter(session, &mhfpacket.MsgMhfInfoScenarioCounter{AckHandle: 37})
		ack := licenseHandlerAck(t, session)
		if ack.AckHandle != 37 || ack.ErrorCode != 0 || !ack.IsBufferResponse || len(ack.AckData) != 1+test.want*6 || int(ack.AckData[0]) != test.want {
			t.Fatal("public counter wire response mismatch")
		}
		actual := make(map[Scenario]bool)
		var previous Scenario
		for i := 0; i < test.want; i++ {
			at := 1 + i*6
			row := Scenario{MainID: binary.BigEndian.Uint32(ack.AckData[at : at+4]), CategoryID: ack.AckData[at+5]}
			if actual[row] {
				t.Fatal("counter wire duplicated a row")
			}
			actual[row] = true
			exchange := row.CategoryID == 3 || row.CategoryID == 6 || row.CategoryID == 7
			if (ack.AckData[at+4] != 0) != exchange {
				t.Fatal("counter exchange flag changed")
			}
			if test.enabled && i > 0 && (row.CategoryID < previous.CategoryID || row.CategoryID == previous.CategoryID && row.MainID <= previous.MainID) {
				t.Fatal("enabled roster is not sorted by category and main ID")
			}
			previous = row
		}
		for _, row := range fixtureRows[:test.want] {
			if !actual[row] {
				t.Fatal("counter wire lost an expected original row")
			}
		}
		if actual[Scenario{MainID: 37, CategoryID: 1}] != (test.enabled && test.addOriginal) {
			t.Fatal("proposed original counter inclusion mismatch")
		}
		if !reflect.DeepEqual(before, fixtureRows) {
			t.Fatal("handler mutated source counter rows")
		}
	}

	server := createMockServer()
	server.erupeConfig.RealClientMode = cfg.ZZ
	server.erupeConfig.QuestDelivery.Enabled = true
	server.erupeConfig.BinPath = binPath
	session := createMockSession(1, server)
	fileBytes := 0
	fileSet := make(map[string]bool)
	expectedFiles := map[string]bool{"1_0_0_0_S37_T1_C0.bin": true}
	for chapter := 0; chapter <= 5; chapter++ {
		expectedFiles[fmt.Sprintf("1_0_0_0_S37_T3_C%d.bin", chapter)] = true
		if chapter < 5 {
			expectedFiles[fmt.Sprintf("1_0_0_0_S37_T2_C%d.bin", chapter)] = true
		}
	}
	for _, row := range input.Files {
		var category, main, flags, chapter int
		if n, err := fmt.Sscanf(row.Name, "%d_0_0_0_S%d_T%d_C%d.bin", &category, &main, &flags, &chapter); err != nil || n != 4 {
			t.Fatal("invalid public S37 filename", row.Name, err)
		}
		if category != 1 || main != 37 || !expectedFiles[row.Name] || fileSet[row.Name] {
			t.Fatal("capture contains an unexpected S37 file")
		}
		fileSet[row.Name] = true
		data, err := os.ReadFile(filepath.Join(binPath, "scenarios", row.Name))
		if err != nil || len(data) != row.Bytes {
			t.Fatal("captured file length mismatch", row.Name, err)
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != row.SHA256 {
			t.Fatal("captured scenario SHA mismatch", row.Name)
		}
		// Serialize the actual request payload, then use the real packet parser
		// rather than constructing its unexported identifier type.
		bf := byteframe.NewByteFrame()
		ackHandle := 3700 + uint32(flags*10+chapter)
		bf.WriteUint32(ackHandle)
		bf.WriteBool(true)
		bf.WriteUint8(0)
		bf.WriteUint8(uint8(category))
		bf.WriteUint32(uint32(main))
		bf.WriteUint8(uint8(chapter))
		bf.WriteUint8(uint8(flags))
		packet := &mhfpacket.MsgSysGetFile{}
		request := byteframe.NewByteFrameFromBytes(bf.Data())
		if err := packet.Parse(request, nil); err != nil {
			t.Fatal("S37 request parse failed", err)
		}
		if request.Err() != nil || int(request.Index()) != len(bf.Data()) || packet.AckHandle != ackHandle || !packet.IsScenario || packet.Filename != "" || packet.ScenarioIdentifer.CategoryID != 1 || packet.ScenarioIdentifer.MainID != 37 || int(packet.ScenarioIdentifer.Flags) != flags || int(packet.ScenarioIdentifer.ChapterID) != chapter {
			t.Fatal("S37 request parser changed a field or left unread bytes")
		}
		handleMsgSysGetFile(session, packet)
		ack := licenseHandlerAck(t, session)
		if ack.ErrorCode != 0 || ack.AckHandle != packet.AckHandle || !ack.IsBufferResponse || !bytes.Equal(ack.AckData, data) {
			t.Fatal("S37 file was changed or lost in the handler ACK", row.Name)
		}
		fileBytes += len(data)
	}
	for name := range expectedFiles {
		if !fileSet[name] {
			t.Fatal("exact original title/chapter/terminal file is missing", name)
		}
	}
	// A wrong chapter must return the regular failure ACK, not an empty
	// success that would make a missing stage look available.
	missing := &mhfpacket.MsgSysGetFile{AckHandle: 3799, IsScenario: true}
	missing.ScenarioIdentifer.CategoryID = 1
	missing.ScenarioIdentifer.MainID = 37
	missing.ScenarioIdentifer.Flags = 2
	missing.ScenarioIdentifer.ChapterID = 99
	handleMsgSysGetFile(session, missing)
	ack := licenseHandlerAck(t, session)
	if ack.AckHandle != 3799 || ack.ErrorCode == 0 || len(ack.AckData) != 0 {
		t.Fatal("missing original chapter did not fail cleanly")
	}
	report := map[string]interface{}{
		"schema": "quest-scenario37-original-wire-rehearsal-v1", "verified_at_utc": time.Now().UTC(), "passed": true,
		"counter_baseline_rows": 171, "counter_proposed_rows": 172, "counter_legacy_fixture_rows": 128,
		"counter_variants_checked": 3, "source_counter_rows_unchanged": true,
		"actual_request_parser_file_ACKs_checked": len(input.Files), "file_bytes_preserved": fileBytes,
		"missing_chapter_failure_ACK_checked": true, "NAS_file_hashes_verified": len(input.Files),
		"database_or_production_changes": false, "network_or_client_reception": false,
		"counter_registration_applied": false, "NPC_chapter_or_reward_execution": false, "manual_claim_save_test": false,
		"limits": []string{"Previous public master roster plus proposed S37 row; not a fresh operating DB snapshot.",
			"Scenario files use the existing direct file path, separate from event-quest schedule filtering.",
			"No menu, chapter acceptance, linked quest50126, reward2552, repeat eligibility, inventory or SAVE proof."},
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scenario37-wire-rehearsal.json"), append(encoded, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	t.Logf("counter variants=3 file ACKs=%d preserved bytes=%d missing chapter=failed ACK", len(input.Files), fileBytes)
}
