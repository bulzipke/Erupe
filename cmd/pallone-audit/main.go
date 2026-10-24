// pallone-audit inventories both ZZ route catalogues and their quest files.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"erupe-ce/common/mhfpallone"
)

func main() {
	clientData := flag.String("client-data", "", "ZZ mhfdat.bin (raw, ECD, or JPK)")
	binPath := flag.String("bin-path", "bin", "Erupe BinPath containing quests/")
	questInfo := flag.String("quest-info-g10", "", "optional G10.1 JP mhfinf.bin for original quest metadata recovery (not stage files)")
	flag.Parse()
	if *clientData == "" {
		fmt.Fprintln(os.Stderr, "usage: pallone-audit -client-data path/to/mhfdat.bin [-bin-path bin] [-quest-info-g10 path/to/mhfinf.bin]")
		os.Exit(2)
	}
	if err := run(*clientData, *binPath, *questInfo); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(path, binPath, questInfoPath string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sourceHash := fmt.Sprintf("%x", sha256.Sum256(data))
	decoded, err := mhfpallone.DecodeClientData(data)
	if err != nil {
		return err
	}
	retained, err := mhfpallone.ParseCatalogue(decoded)
	if err != nil {
		return err
	}
	banks, err := mhfpallone.ParseRouteBanks(decoded)
	if err != nil {
		return err
	}
	retainedAudit, err := mhfpallone.AuditAssets(retained, binPath)
	if err != nil {
		return err
	}
	bankAudit, err := mhfpallone.AuditAssets(banks, binPath)
	if err != nil {
		return err
	}
	result := struct {
		SourceSHA256  string                `json:"source_sha256"`
		DecodedSHA256 string                `json:"decoded_sha256"`
		Retained      mhfpallone.AssetAudit `json:"retained_catalogue_0x878"`
		RouteBanks    mhfpallone.AssetAudit `json:"route_banks_0x91c"`
		QuestInfo     *questInfoEvidence    `json:"g10_quest_metadata,omitempty"`
	}{SourceSHA256: sourceHash, DecodedSHA256: fmt.Sprintf("%x", sha256.Sum256(decoded)), Retained: retainedAudit, RouteBanks: bankAudit}
	if questInfoPath != "" {
		input, err := os.ReadFile(questInfoPath)
		if err != nil {
			return err
		}
		raw, err := mhfpallone.DecodeQuestInfoG10(input)
		if err != nil {
			return err
		}
		var ids []uint16
		for _, route := range retained {
			ids = append(ids, route.StageQuestIDs...)
		}
		recovery, err := mhfpallone.RecoverQuestInfoG10(raw, ids)
		if err != nil {
			return err
		}
		result.QuestInfo = &questInfoEvidence{SourceSHA256: fmt.Sprintf("%x", sha256.Sum256(input)), DecodedSHA256: fmt.Sprintf("%x", sha256.Sum256(raw)), Recovery: recovery}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

type questInfoEvidence struct {
	SourceSHA256  string                       `json:"source_sha256"`
	DecodedSHA256 string                       `json:"decoded_sha256"`
	Recovery      mhfpallone.QuestInfoRecovery `json:"recovery"`
}
