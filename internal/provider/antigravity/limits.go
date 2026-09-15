package antigravity

import (
	"agentcli.local/ai/internal/provider"
	"fmt"
	"os"
	"path/filepath"
)

// GetLimits reads the Antigravity IDE state database to surface AI credit
// balance, plan name, and minimum credit threshold — all stored locally in
// antigravityUnifiedStateSync.userStatus and .modelCredits.
func (p *AntigravityProvider) GetLimits() ([]provider.LimitInfo, error) {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".config/Antigravity IDE/User/globalStorage/state.vscdb")
	var limits []provider.LimitInfo

	// ── modelCredits ──────────────────────────────────────────────────────
	// Reads: useAICredits (bool), availableCredits (int), minimumCreditAmount (int)
	useCredits := false
	availableCredits := int64(-1)
	minimumCredits := int64(-1)

	if raw, err := readVscdbB64Proto(dbPath, "antigravityUnifiedStateSync.modelCredits"); err == nil {
		sm := extractSentinelMap(raw)
		if v, ok := sm["useAICreditsSentinelKey"]; ok {
			for _, f := range parseMsg(v) {
				if f.fieldNum == 1 && f.wireType == 0 {
					useCredits = f.varint == 1
				}
			}
		}
		if v, ok := sm["availableCreditsSentinelKey"]; ok {
			for _, f := range parseMsg(v) {
				if f.fieldNum == 2 && f.wireType == 0 {
					availableCredits = int64(f.varint)
				}
			}
		}
		if v, ok := sm["minimumCreditAmountForUsageKey"]; ok {
			for _, f := range parseMsg(v) {
				if f.fieldNum == 2 && f.wireType == 0 {
					minimumCredits = int64(f.varint)
				}
			}
		}
	}

	// ── userStatus ────────────────────────────────────────────────────────
	// field36 = subscription/plan info sub-message
	//   sub_field1  = plan ID  (e.g. "g1-pro-tier")
	//   sub_field2  = plan name (e.g. "Google AI Pro")
	//   sub_field14 = credits sub-proto { field1=?, field2=available, field3=minimum }
	planID := ""
	planName := ""
	creditsFromStatus := int64(-1)

	if raw, err := readVscdbB64Proto(dbPath, "antigravityUnifiedStateSync.userStatus"); err == nil {
		sm := extractSentinelMap(raw)
		if v, ok := sm["userStatusSentinelKey"]; ok {
			for _, topF := range parseMsg(v) {
				if topF.fieldNum == 36 && topF.wireType == 2 {
					// plan info sub-message
					for _, pf := range parseMsg(topF.data) {
						switch {
						case pf.fieldNum == 1 && pf.wireType == 2:
							planID = string(pf.data)
						case pf.fieldNum == 2 && pf.wireType == 2:
							planName = string(pf.data)
						case pf.fieldNum == 14 && pf.wireType == 2:
							// credits sub-proto: field2=available, field3=minimum
							for _, cf := range parseMsg(pf.data) {
								if cf.fieldNum == 2 && cf.wireType == 0 {
									creditsFromStatus = int64(cf.varint)
								}
								if cf.fieldNum == 3 && cf.wireType == 0 && minimumCredits < 0 {
									minimumCredits = int64(cf.varint)
								}
							}
						}
					}
					break // only process first field36
				}
			}
		}
	}

	// Prefer the credit value from userStatus (more reliable)
	if creditsFromStatus >= 0 {
		availableCredits = creditsFromStatus
	}

	// ── Emit limits ───────────────────────────────────────────────────────
	planLabel := planID
	if planName != "" {
		planLabel = planName
		if planID != "" {
			planLabel = planName + " (" + planID + ")"
		}
	}
	if planLabel == "" {
		planLabel = "unknown"
	}

	limits = append(limits, provider.LimitInfo{
		Provider:    p.Name(),
		Name:        "Subscription plan",
		Used:        -1,
		Limit:       -1,
		UsedPct:     -1,
		Unit:        "-",
		RefillEvery: "N/A",
		Note:        planLabel,
	})

	if useCredits || availableCredits >= 0 {
		creditNote := ""
		usedPct := float64(-1)
		if availableCredits >= 0 && minimumCredits > 0 {
			creditNote = fmt.Sprintf("min required: %d", minimumCredits)
		}
		limits = append(limits, provider.LimitInfo{
			Provider:    p.Name(),
			Name:        "Available AI Credits",
			Used:        -1,
			Limit:       availableCredits,
			UsedPct:     usedPct,
			Unit:        "credits",
			RefillEvery: "N/A",
			Note:        creditNote,
		})
	} else {
		limits = append(limits, provider.LimitInfo{
			Provider:    p.Name(),
			Name:        "AI Credits",
			Used:        -1,
			Limit:       -1,
			UsedPct:     -1,
			Unit:        "-",
			RefillEvery: "N/A",
			Note:        "credits not in use or not available locally",
		})
	}

	limits = append(limits, provider.LimitInfo{
		Provider:    p.Name(),
		Name:        "Token / request quota",
		Used:        -1,
		Limit:       -1,
		UsedPct:     -1,
		Unit:        "-",
		RefillEvery: "N/A",
		Note:        "Not tracked locally — check aistudio.google.com/app/apikey or console.cloud.google.com",
	})

	return limits, nil
}
