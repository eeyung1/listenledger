package api

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
)

type IndependentRecordInput struct {
	OccurredAt string `json:"occurred_at"` // ISO8601
	SKUOrName  string `json:"sku_or_name"`
	Type       string `json:"type"` // "sale" or "restock"
	Quantity   int    `json:"quantity"`
	Unit       string `json:"unit"`
	Amount     int64  `json:"amount"` // in minor units (e.g. kobo)
}

type PhysicalStockCountInput struct {
	SKUOrName     string `json:"sku_or_name"`
	PhysicalCount int    `json:"physical_count"`
}

type ReconcileRequest struct {
	PeriodStart         string                    `json:"period_start"`
	PeriodEnd           string                    `json:"period_end"`
	IndependentSource   string                    `json:"independent_source"`
	IndependentRecords  []IndependentRecordInput  `json:"independent_records"`
	PhysicalStockCounts []PhysicalStockCountInput `json:"physical_stock_counts"`
	Notes               string                    `json:"notes"`
	ConductedBy         string                    `json:"conducted_by"`
}

type GateCheckResult struct {
	Metric   string  `json:"metric"`
	Target   string  `json:"target"`
	Achieved float64 `json:"achieved"`
	Passed   bool    `json:"passed"`
}

type ReconcileResponse struct {
	Reconciliation storage.PilotReconciliation `json:"reconciliation"`
	GateResults    []GateCheckResult           `json:"gate_results"`
	AllGatesPassed bool                        `json:"all_gates_passed"`
}

func (s *Server) handlePilotReconcile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}

	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	var req ReconcileRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.PeriodStart == "" || req.PeriodEnd == "" {
		writeError(w, http.StatusBadRequest, "period_start and period_end are required")
		return
	}

	periodStart, err := time.Parse(time.RFC3339, req.PeriodStart)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid period_start format; expected RFC3339")
		return
	}
	periodEnd, err := time.Parse(time.RFC3339, req.PeriodEnd)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid period_end format; expected RFC3339")
		return
	}
	if periodEnd.Before(periodStart) {
		writeError(w, http.StatusBadRequest, "period_end must be greater than or equal to period_start")
		return
	}

	if req.IndependentSource == "" {
		req.IndependentSource = "Paper Counter Ledger"
	}
	if req.ConductedBy == "" {
		req.ConductedBy = "Shop Owner / Shadow Auditor"
	}

	ctx := r.Context()

	// 1. Load catalogue products
	products, err := s.store.ListProducts(ctx, shopID)
	if err != nil {
		s.fail(w, r, "list products for reconciliation", err)
		return
	}

	skuMap := make(map[string]models.Product)
	nameMap := make(map[string]models.Product)
	for _, p := range products {
		skuMap[strings.ToLower(strings.TrimSpace(p.SKU))] = p
		nameMap[strings.ToLower(strings.TrimSpace(p.Name))] = p
		for _, alias := range p.Aliases {
			nameMap[strings.ToLower(strings.TrimSpace(alias))] = p
		}
	}

	resolveProduct := func(key string) (models.Product, bool) {
		clean := strings.ToLower(strings.TrimSpace(key))
		if p, ok := skuMap[clean]; ok {
			return p, true
		}
		if p, ok := nameMap[clean]; ok {
			return p, true
		}
		return models.Product{}, false
	}

	// 2. Load confirmed transactions in period
	statusConfirmed := models.StatusConfirmed
	txns, _, err := s.store.QueryTransactions(ctx, shopID, storage.TransactionFilter{
		From:   &periodStart,
		To:     &periodEnd,
		Status: &statusConfirmed,
		Limit:  storage.MaxTransactionLimit,
	})
	if err != nil {
		s.fail(w, r, "query transactions for reconciliation", err)
		return
	}

	// 3. Match independent records against confirmed transactions
	recID, err := newID("prc_")
	if err != nil {
		s.fail(w, r, "generate reconciliation id", err)
		return
	}

	var discrepancies []storage.PilotDiscrepancy
	matchedTxnIDs := make(map[string]bool)
	matchedRecords := 0
	missedCaptures := 0
	priceDiscrepancies := 0
	quantityDiscrepancies := 0

	var totalIndepRevenue int64
	var totalCapturedRevenue int64

	// Sum total captured revenue
	for _, tx := range txns {
		if tx.Type == models.TypeSale {
			totalCapturedRevenue += tx.Amount.Amount
		}
	}

	// Process independent entries
	for _, ind := range req.IndependentRecords {
		indType := strings.ToLower(strings.TrimSpace(ind.Type))
		if indType == "sale" {
			totalIndepRevenue += ind.Amount
		}

		indProduct, productFound := resolveProduct(ind.SKUOrName)
		prodName := ind.SKUOrName
		var prodID *string
		if productFound {
			prodName = indProduct.Name
			idCopy := indProduct.ID
			prodID = &idCopy
		}

		var indTime time.Time
		if ind.OccurredAt != "" {
			t, err := time.Parse(time.RFC3339, ind.OccurredAt)
			if err == nil {
				indTime = t
			}
		}

		// Look for best matching transaction
		var bestMatch *models.Transaction
		var bestDistance time.Duration = time.Hour * 24

		for i := range txns {
			tx := &txns[i]
			if matchedTxnIDs[tx.ID] {
				continue
			}
			if string(tx.Type) != indType {
				continue
			}

			// Check product equivalence
			txProductMatches := false
			if prodID != nil && *prodID == tx.ProductID {
				txProductMatches = true
			} else if strings.EqualFold(tx.ProductName, prodName) || strings.EqualFold(tx.ProductName, ind.SKUOrName) {
				txProductMatches = true
			}

			if !txProductMatches {
				continue
			}

			// Calculate time distance
			distance := time.Duration(0)
			if !indTime.IsZero() {
				distance = tx.OccurredAt.Sub(indTime)
				if distance < 0 {
					distance = -distance
				}
			}

			// Allow up to 30 minutes window
			if distance <= time.Minute*30 && (bestMatch == nil || distance < bestDistance) {
				bestMatch = tx
				bestDistance = distance
			}
		}

		if bestMatch != nil {
			matchedTxnIDs[bestMatch.ID] = true
			matchedRecords++

			// Check quantity match
			if bestMatch.Quantity != ind.Quantity && ind.Quantity > 0 {
				quantityDiscrepancies++
				dID, _ := newID("pds_")
				discrepancies = append(discrepancies, storage.PilotDiscrepancy{
					ID:                  dID,
					ReconciliationID:    recID,
					ShopID:              shopID,
					DiscrepancyType:     "quantity_mismatch",
					ProductID:           prodID,
					ProductName:         prodName,
					ExpectedQuantity:    ind.Quantity,
					ActualQuantity:      bestMatch.Quantity,
					ExpectedAmount:      ind.Amount,
					ActualAmount:        bestMatch.Amount.Amount,
					TransactionID:       &bestMatch.ID,
					InvestigationStatus: "unexplained",
				})
			}

			// Check amount match
			if bestMatch.Amount.Amount != ind.Amount && ind.Amount > 0 {
				priceDiscrepancies++
				dID, _ := newID("pds_")
				discrepancies = append(discrepancies, storage.PilotDiscrepancy{
					ID:                  dID,
					ReconciliationID:    recID,
					ShopID:              shopID,
					DiscrepancyType:     "price_mismatch",
					ProductID:           prodID,
					ProductName:         prodName,
					ExpectedQuantity:    ind.Quantity,
					ActualQuantity:      bestMatch.Quantity,
					ExpectedAmount:      ind.Amount,
					ActualAmount:        bestMatch.Amount.Amount,
					TransactionID:       &bestMatch.ID,
					InvestigationStatus: "unexplained",
				})
			}
		} else {
			// Independent record was completely missed by ambient capture
			missedCaptures++
			dID, _ := newID("pds_")
			discrepancies = append(discrepancies, storage.PilotDiscrepancy{
				ID:                  dID,
				ReconciliationID:    recID,
				ShopID:              shopID,
				DiscrepancyType:     "missed_capture",
				ProductID:           prodID,
				ProductName:         prodName,
				ExpectedQuantity:    ind.Quantity,
				ActualQuantity:      0,
				ExpectedAmount:      ind.Amount,
				ActualAmount:        0,
				InvestigationStatus: "unexplained",
			})
		}
	}

	// 4. Any confirmed transactions without a matching independent record are false captures
	falseCaptures := 0
	for i := range txns {
		tx := &txns[i]
		if !matchedTxnIDs[tx.ID] {
			falseCaptures++
			dID, _ := newID("pds_")
			var txProdID *string
			if tx.ProductID != "" {
				idCopy := tx.ProductID
				txProdID = &idCopy
			}
			discrepancies = append(discrepancies, storage.PilotDiscrepancy{
				ID:                  dID,
				ReconciliationID:    recID,
				ShopID:              shopID,
				DiscrepancyType:     "false_capture",
				ProductID:           txProdID,
				ProductName:         tx.ProductName,
				ExpectedQuantity:    0,
				ActualQuantity:      tx.Quantity,
				ExpectedAmount:      0,
				ActualAmount:        tx.Amount.Amount,
				TransactionID:       &tx.ID,
				InvestigationStatus: "unexplained",
			})
		}
	}

	// 5. Physical Stock Count Verification
	stockDiscrepancyCount := 0
	for _, sc := range req.PhysicalStockCounts {
		prod, found := resolveProduct(sc.SKUOrName)
		prodName := sc.SKUOrName
		var prodID *string
		if found {
			prodName = prod.Name
			idCopy := prod.ID
			prodID = &idCopy
		}

		if found && prod.Quantity != sc.PhysicalCount {
			stockDiscrepancyCount++
			dID, _ := newID("pds_")
			discrepancies = append(discrepancies, storage.PilotDiscrepancy{
				ID:                  dID,
				ReconciliationID:    recID,
				ShopID:              shopID,
				DiscrepancyType:     "stock_variance",
				ProductID:           prodID,
				ProductName:         prodName,
				ExpectedQuantity:    sc.PhysicalCount, // Ground truth
				ActualQuantity:      prod.Quantity,    // In system catalogue
				InvestigationStatus: "unexplained",
			})
		}
	}

	revenueDiscrepancy := totalIndepRevenue - totalCapturedRevenue
	if revenueDiscrepancy < 0 {
		revenueDiscrepancy = -revenueDiscrepancy
	}

	// Session status: 'reconciled' if 0 discrepancies, else 'discrepant'
	recStatus := "reconciled"
	if len(discrepancies) > 0 {
		recStatus = "discrepant"
	}

	now := time.Now().UTC()
	rec := storage.PilotReconciliation{
		ID:                        recID,
		ShopID:                    shopID,
		Status:                    recStatus,
		PeriodStart:               periodStart,
		PeriodEnd:                 periodEnd,
		IndependentSource:         req.IndependentSource,
		TotalIndependentRecords:   len(req.IndependentRecords),
		TotalCapturedTransactions: len(txns),
		MatchedRecords:            matchedRecords,
		MissedCaptures:            missedCaptures,
		FalseCaptures:             falseCaptures,
		PriceDiscrepancies:        priceDiscrepancies,
		QuantityDiscrepancies:     quantityDiscrepancies,
		TotalIndependentRevenue:   totalIndepRevenue,
		TotalCapturedRevenue:      totalCapturedRevenue,
		RevenueDiscrepancy:        revenueDiscrepancy,
		StockDiscrepancyCount:     stockDiscrepancyCount,
		Notes:                     req.Notes,
		ConductedBy:               req.ConductedBy,
		CreatedAt:                 now,
	}

	if err := s.store.CreateReconciliation(ctx, rec, discrepancies); err != nil {
		s.fail(w, r, "persist reconciliation", err)
		return
	}
	rec.Discrepancies = discrepancies

	// 6. Evaluate Target Gates
	gates, _, err := s.store.GetTargetGates(ctx, shopID)
	if err != nil {
		gates = models.DefaultTargetGates
	}

	totalInd := len(req.IndependentRecords)
	var fullAcc float64 = 100.0
	if totalInd > 0 {
		fullAcc = (float64(matchedRecords) / float64(totalInd)) * 100.0
	}

	var falseCapRate float64 = 0.0
	if len(txns) > 0 {
		falseCapRate = (float64(falseCaptures) / float64(len(txns))) * 100.0
	}

	var missedRate float64 = 0.0
	if totalInd > 0 {
		missedRate = (float64(missedCaptures) / float64(totalInd)) * 100.0
	}

	gateResults := []GateCheckResult{
		{
			Metric:   "Full Transaction Accuracy",
			Target:   ">= " + formatPercent(gates.MinFullTransactionAccuracy),
			Achieved: math.Round(fullAcc*10) / 10,
			Passed:   fullAcc >= gates.MinFullTransactionAccuracy,
		},
		{
			Metric:   "False Capture Rate",
			Target:   "<= " + formatPercent(gates.MaxFalseCaptureRate),
			Achieved: math.Round(falseCapRate*10) / 10,
			Passed:   falseCapRate <= gates.MaxFalseCaptureRate,
		},
		{
			Metric:   "Missed Transaction Rate",
			Target:   "<= " + formatPercent(gates.MaxMissedTransactionRate),
			Achieved: math.Round(missedRate*10) / 10,
			Passed:   missedRate <= gates.MaxMissedTransactionRate,
		},
		{
			Metric:   "Unexplained Revenue Leakage",
			Target:   "0 discrepancy",
			Achieved: float64(revenueDiscrepancy),
			Passed:   revenueDiscrepancy == 0,
		},
		{
			Metric:   "Unexplained Inventory Discrepancies",
			Target:   "0 discrepancies",
			Achieved: float64(stockDiscrepancyCount),
			Passed:   stockDiscrepancyCount == 0,
		},
	}

	allPassed := true
	for _, gr := range gateResults {
		if !gr.Passed {
			allPassed = false
			break
		}
	}

	writeJSON(w, http.StatusCreated, ReconcileResponse{
		Reconciliation: rec,
		GateResults:    gateResults,
		AllGatesPassed: allPassed,
	})
}

func (s *Server) handlePilotReconciliations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}

	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	list, err := s.store.ListReconciliations(r.Context(), shopID, 50, 0)
	if err != nil {
		s.fail(w, r, "list reconciliations", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"shop_id":         shopID,
		"reconciliations": list,
	})
}

func (s *Server) handlePilotReconciliationDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}

	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/pilot/reconciliations/")
	id = strings.Trim(id, "/")

	rec, err := s.store.GetReconciliation(r.Context(), shopID, id)
	if err != nil {
		if err == storage.ErrNotFound {
			writeError(w, http.StatusNotFound, "reconciliation not found")
			return
		}
		s.fail(w, r, "get reconciliation", err)
		return
	}

	writeJSON(w, http.StatusOK, rec)
}

type InvestigateDiscrepancyRequest struct {
	RootCause          string `json:"root_cause"`
	InvestigationNotes string `json:"investigation_notes"`
	InvestigatedBy     string `json:"investigated_by"`
}

func (s *Server) handlePilotDiscrepancyDetail(w http.ResponseWriter, r *http.Request) {
	// e.g. /api/pilot/discrepancies/{id}/investigate
	path := strings.TrimPrefix(r.URL.Path, "/api/pilot/discrepancies/")
	parts := strings.Split(strings.Trim(path, "/"), "/")

	if len(parts) != 2 || parts[1] != "investigate" || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "discrepancy action not found")
		return
	}

	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	discrepancyID := parts[0]
	var req InvestigateDiscrepancyRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	validRootCauses := map[string]bool{
		"acoustic_noise":         true,
		"speech_boundary_miss":   true,
		"cashier_protocol_drift": true,
		"customer_cancellation":  true,
		"physical_shrinkage":     true,
		"supplier_shortage":      true,
		"other":                  true,
	}

	rootCause := strings.TrimSpace(req.RootCause)
	if !validRootCauses[rootCause] {
		writeError(w, http.StatusBadRequest, "invalid root_cause; expected one of acoustic_noise, speech_boundary_miss, cashier_protocol_drift, customer_cancellation, physical_shrinkage, supplier_shortage, other")
		return
	}

	if req.InvestigatedBy == "" {
		req.InvestigatedBy = "Shop Owner / Investigator"
	}

	now := time.Now().UTC()
	err := s.store.UpdateDiscrepancyInvestigation(r.Context(), shopID, discrepancyID, rootCause, req.InvestigationNotes, req.InvestigatedBy, now)
	if err != nil {
		if err == storage.ErrNotFound {
			writeError(w, http.StatusNotFound, "discrepancy not found")
			return
		}
		s.fail(w, r, "update discrepancy investigation", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"discrepancy_id":       discrepancyID,
		"investigation_status": "investigated",
		"root_cause":           rootCause,
		"investigated_at":      now.Format(time.RFC3339),
	})
}

func (s *Server) handlePilotCohort(w http.ResponseWriter, r *http.Request) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		cohort, err := s.store.GetShopCohort(r.Context(), shopID)
		if err != nil {
			s.fail(w, r, "get shop cohort", err)
			return
		}
		writeJSON(w, http.StatusOK, cohort)

	case http.MethodPost:
		var req struct {
			CohortPhase string `json:"cohort_phase"`
			StageStatus string `json:"stage_status"`
			Notes       string `json:"notes"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}

		cohort := storage.ShopCohort{
			ShopID:      shopID,
			CohortPhase: req.CohortPhase,
			StageStatus: req.StageStatus,
			Notes:       req.Notes,
		}
		if err := s.store.UpsertShopCohort(r.Context(), cohort); err != nil {
			s.fail(w, r, "upsert shop cohort", err)
			return
		}
		writeJSON(w, http.StatusOK, cohort)

	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

type ReadinessCheckItem struct {
	Requirement string `json:"requirement"`
	Satisfied   bool   `json:"satisfied"`
	Details     string `json:"details"`
}

type CohortReadinessResponse struct {
	ShopID             string               `json:"shop_id"`
	CurrentPhase       string               `json:"current_phase"`
	NextPhase          string               `json:"next_phase"`
	ReadinessStatus    string               `json:"readiness_status"` // "qualified", "blocked"
	Checks             []ReadinessCheckItem `json:"checks"`
	AllChecksSatisfied bool                 `json:"all_checks_satisfied"`
}

func (s *Server) handlePilotCohortEvaluateReadiness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}

	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	var req struct {
		Promote bool `json:"promote"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	ctx := r.Context()

	// 1. Check current cohort phase
	cohort, err := s.store.GetShopCohort(ctx, shopID)
	if err != nil {
		s.fail(w, r, "get shop cohort", err)
		return
	}

	// 2. Check staff training
	shop, err := s.store.GetShop(ctx, shopID)
	if err != nil {
		s.fail(w, r, "get shop", err)
		return
	}
	staffTrained := shop.StaffTrainedAt != nil

	// 3. Check agreed target gates
	_, gatesAgreed, err := s.store.GetTargetGates(ctx, shopID)
	if err != nil {
		s.fail(w, r, "get target gates", err)
		return
	}

	// 4. Check reconciliation history
	recs, err := s.store.ListReconciliations(ctx, shopID, 1, 0)
	if err != nil {
		s.fail(w, r, "list reconciliations", err)
		return
	}

	hasReconciliation := len(recs) > 0
	latestReconciled := false
	zeroUnexplainedDiffs := false
	latestID := "none"

	if hasReconciliation {
		latestRec, err := s.store.GetReconciliation(ctx, shopID, recs[0].ID)
		if err == nil {
			latestID = latestRec.ID
			unexplainedCount := 0
			for _, d := range latestRec.Discrepancies {
				if d.InvestigationStatus == "unexplained" {
					unexplainedCount++
				}
			}
			if unexplainedCount == 0 {
				zeroUnexplainedDiffs = true
			}
			if latestRec.Status == "reconciled" {
				latestReconciled = true
			}
		}
	}

	checks := []ReadinessCheckItem{
		{
			Requirement: "Cashier Protocol Training & Boundary Drill Completed",
			Satisfied:   staffTrained,
			Details:     formatCheckDetails(staffTrained, "Cashier protocol drill completed", "Staff training drill pending"),
		},
		{
			Requirement: "Owner Target Gates Agreed",
			Satisfied:   gatesAgreed,
			Details:     formatCheckDetails(gatesAgreed, "Target release gates agreed by owner", "Target release gates agreement pending"),
		},
		{
			Requirement: "At Least 1 Dual-Entry Pilot Reconciliation Performed",
			Satisfied:   hasReconciliation,
			Details:     formatCheckDetails(hasReconciliation, "Reconciliation session "+latestID+" executed", "No dual-entry trial reconciliation recorded"),
		},
		{
			Requirement: "Zero Unexplained Discrepancies (All Differences Investigated)",
			Satisfied:   zeroUnexplainedDiffs,
			Details:     formatCheckDetails(zeroUnexplainedDiffs, "All differences investigated with documented root causes", "Unresolved/unexplained discrepancies remain"),
		},
		{
			Requirement: "Latest Trial Reconciliation Formally Reconciled",
			Satisfied:   latestReconciled,
			Details:     formatCheckDetails(latestReconciled, "Session marked reconciled", "Reconciliation session discrepant"),
		},
	}

	allSatisfied := true
	for _, c := range checks {
		if !c.Satisfied {
			allSatisfied = false
			break
		}
	}

	readinessStatus := "blocked"
	if allSatisfied {
		readinessStatus = "qualified"
		now := time.Now().UTC()
		cohort.StageStatus = "qualified"
		cohort.QualifiedAt = &now
		if req.Promote && cohort.CohortPhase == "phase1_controlled" {
			cohort.CohortPhase = "phase2_dual_shop"
			cohort.Notes = "Promoted to Phase 2 dual-shop expansion after zero unexplained discrepancies."
		}
		_ = s.store.UpsertShopCohort(ctx, cohort)
	}

	nextPhase := "phase2_dual_shop"
	if cohort.CohortPhase == "phase2_dual_shop" {
		nextPhase = "phase3_expanded_5"
	} else if cohort.CohortPhase == "phase3_expanded_5" {
		nextPhase = "phase4_scaled_15"
	}

	writeJSON(w, http.StatusOK, CohortReadinessResponse{
		ShopID:             shopID,
		CurrentPhase:       cohort.CohortPhase,
		NextPhase:          nextPhase,
		ReadinessStatus:    readinessStatus,
		Checks:             checks,
		AllChecksSatisfied: allSatisfied,
	})
}

func formatPercent(v float64) string {
	return fmt.Sprintf("%.1f%%", v)
}

func formatCheckDetails(satisfied bool, passMsg, failMsg string) string {
	if satisfied {
		return passMsg
	}
	return failMsg
}
