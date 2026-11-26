package social_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/smarniw/metamorphosis/internal/domain"
	"github.com/smarniw/metamorphosis/internal/social"
)

// These rules are decided before anything is read, so a service with no database is enough.
func service() *social.Service { return social.NewService(nil, nil) }

func kind(err error) domain.Kind {
	var de *domain.Error
	if errors.As(err, &de) {
		return de.Kind
	}
	return 0
}

func TestSearchNeedsThreeCharacters(t *testing.T) {
	for _, q := range []string{"", "  ", "ab", " a ", "é"} {
		if _, err := service().Search(context.Background(), uuid.New(), q); kind(err) != domain.KindInvalid {
			t.Errorf("%q: got %v, want invalid", q, err)
		}
	}
}

func TestYouCannotDoSocialThingsToYourself(t *testing.T) {
	ctx, me := context.Background(), uuid.New()
	if _, err := service().SendRequest(ctx, me, me); kind(err) != domain.KindInvalid {
		t.Errorf("add yourself: %v", err)
	}
	if err := service().Block(ctx, me, me); kind(err) != domain.KindInvalid {
		t.Errorf("block yourself: %v", err)
	}
	if _, err := service().Like(ctx, me, me, uuid.New()); kind(err) != domain.KindInvalid {
		t.Errorf("like your own workout: %v", err)
	}
	if err := service().Report(ctx, me, social.ReportInput{UserID: me, Reason: "me"}); kind(err) != domain.KindInvalid {
		t.Errorf("report yourself: %v", err)
	}
}

func TestReportReasonIsRequiredAndBounded(t *testing.T) {
	ctx := context.Background()
	for _, reason := range []string{"", "   \n", strings.Repeat("x", 501)} {
		err := service().Report(ctx, uuid.New(), social.ReportInput{UserID: uuid.New(), Reason: reason})
		if kind(err) != domain.KindInvalid {
			t.Errorf("%d chars: got %v, want invalid", len(reason), err)
		}
	}
}

func TestReportStatusMustBeOpenOrResolved(t *testing.T) {
	if _, err := service().Reports(context.Background(), uuid.New(), "bogus"); kind(err) != domain.KindInvalid {
		t.Fatalf("got %v", err)
	}
}
