package database_test

import (
	"context"
	"testing"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/doctor"
)

func TestDoctor(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, nil)
	report := doctor.Run(context.Background(), database.Doctor(db))
	for _, res := range report.Results {
		if res.Status != doctor.Passed {
			t.Errorf("%s = %+v, want passed on the test database", res.Check, res)
		}
	}
	if len(report.Results) != 3 {
		t.Errorf("results = %+v", report.Results)
	}

	closed := dbtest.Open(t, nil)
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	report = doctor.Run(context.Background(), database.Doctor(closed))
	if report.Results[0].Status != doctor.Failed || report.Results[1].Status != doctor.Skipped || report.Results[2].Status != doctor.Skipped {
		t.Errorf("closed database: %+v", report.Results)
	}
}
