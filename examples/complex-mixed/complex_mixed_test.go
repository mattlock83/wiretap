package complexmixed

import (
	"fmt"
	"testing"
)

func TestComplexMixed(t *testing.T) {
	runtime := newRuntime(t)

	tests := []struct {
		name string
		run  func(*scenarioRuntime) error
	}{
		{name: "complex-mixed-submit-success", run: scenarioSubmitSuccess},
		{name: "complex-mixed-too-high-6000", run: scenarioAmountTooHigh6000},
		{name: "complex-mixed-too-high-15000", run: scenarioAmountTooHigh15000},
		{name: "complex-mixed-insufficient-balance", run: scenarioInsufficientBalance},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if err := runtime.runCase(tc.name, tc.run); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func scenarioSubmitSuccess(r *scenarioRuntime) error {
	if err := r.registerAccounts(r.defaultFromAccount, r.defaultToAccount); err != nil {
		return err
	}

	submitResp, err := r.submit(r.defaultFromAccount.ID, r.defaultToAccount.ID, 120, "USD")
	if err != nil {
		return err
	}
	if got := r.paymentString(submitResp, "status"); got != "submitted" {
		return fmt.Errorf("expected status submitted, got %q", got)
	}
	txID := r.paymentString(submitResp, "transaction_id")
	if txID == "" {
		return fmt.Errorf("transaction_id missing in submit response")
	}

	list, err := r.listTransactions(r.defaultFromAccount.ID)
	if err != nil {
		return err
	}
	if list.Account["id"] != r.defaultFromAccount.ID {
		return fmt.Errorf("expected account %q, got %v", r.defaultFromAccount.ID, list.Account["id"])
	}

	found := false
	for _, tx := range list.Transactions {
		if tx["transaction_id"] == txID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("transaction %q not found in list response", txID)
	}

	return nil
}

func scenarioAmountTooHigh6000(r *scenarioRuntime) error {
	if err := r.registerAccounts(r.highBalanceAccount, r.defaultToAccount); err != nil {
		return err
	}

	resp, err := r.submit(r.highBalanceAccount.ID, r.defaultToAccount.ID, 6000, "USD")
	if err != nil {
		return err
	}
	if r.paymentString(resp, "status") != "rejected" {
		return fmt.Errorf("expected rejected status, got %q", r.paymentString(resp, "status"))
	}
	if r.paymentString(resp, "error_code") != "amount_too_high" {
		return fmt.Errorf("expected amount_too_high, got %q", r.paymentString(resp, "error_code"))
	}
	return nil
}

func scenarioAmountTooHigh15000(r *scenarioRuntime) error {
	if err := r.registerAccounts(r.highBalanceAccount, r.defaultToAccount); err != nil {
		return err
	}

	resp, err := r.submit(r.highBalanceAccount.ID, r.defaultToAccount.ID, 15000, "USD")
	if err != nil {
		return err
	}
	if r.paymentString(resp, "status") != "rejected" {
		return fmt.Errorf("expected rejected status, got %q", r.paymentString(resp, "status"))
	}
	if r.paymentString(resp, "error_code") != "amount_too_high" {
		return fmt.Errorf("expected amount_too_high, got %q", r.paymentString(resp, "error_code"))
	}
	return nil
}

func scenarioInsufficientBalance(r *scenarioRuntime) error {
	if err := r.registerAccounts(r.lowBalanceAccount, r.defaultToAccount); err != nil {
		return err
	}

	resp, err := r.submit(r.lowBalanceAccount.ID, r.defaultToAccount.ID, 75, "USD")
	if err != nil {
		return err
	}
	if r.paymentString(resp, "status") != "rejected" {
		return fmt.Errorf("expected rejected status, got %q", r.paymentString(resp, "status"))
	}
	if r.paymentString(resp, "error_code") != "insufficient_balance" {
		return fmt.Errorf("expected insufficient_balance, got %q", r.paymentString(resp, "error_code"))
	}
	return nil
}
