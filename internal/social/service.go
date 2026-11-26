// Package social is people: finding them, friendships, blocking, likes on friends' workouts, notifications and
// reports.
//
// The privacy rules, in one place:
//   - Anyone can be found by name, but a stranger sees only the name, username, member-since date and workout
//     count. The avatar (the Google photo) is shown to friends only.
//   - Friends (mutual, accepted) can read each other's workouts, including where they happened. Workout notes,
//     body weight and email are never shared.
//   - Someone who has blocked you does not exist as far as you can tell: every lookup answers not found.
package social

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smarniw/metamorphosis/internal/db"
	"github.com/smarniw/metamorphosis/internal/domain"
)

// How a person relates to the caller.
const (
	RelNone            = "none"
	RelFriends         = "friends"
	RelRequestSent     = "request_sent"
	RelRequestReceived = "request_received"
	RelBlocked         = "blocked" // blocked by the caller
	RelSelf            = "self"
)

// Notification kinds.
const (
	NoteFriendRequest  = "friend_request"
	NoteFriendAccepted = "friend_accepted"
	NoteWorkoutLiked   = "workout_liked"
)

const (
	minSearchChars = 3
	maxSearchChars = 50
	searchRows     = 20

	maxRequestsPerHour = 20
	maxReportsPerHour  = 10
	rateWindow         = time.Hour

	maxReasonChars   = 500
	defaultNotifRows = 30
	maxNotifRows     = 100
	maxReportRows    = 100
)

// Store is the database access the service needs. *db.Queries satisfies it.
type Store interface {
	SearchPeople(ctx context.Context, arg db.SearchPeopleParams) ([]db.SearchPeopleRow, error)
	GetPerson(ctx context.Context, arg db.GetPersonParams) (db.GetPersonRow, error)
	ListFriends(ctx context.Context, viewerID uuid.UUID) ([]db.ListFriendsRow, error)
	ListIncomingRequests(ctx context.Context, viewerID uuid.UUID) ([]db.ListIncomingRequestsRow, error)
	ListOutgoingRequests(ctx context.Context, viewerID uuid.UUID) ([]db.ListOutgoingRequestsRow, error)
	ListBlocked(ctx context.Context, viewerID uuid.UUID) ([]db.ListBlockedRow, error)
	LockFriendship(ctx context.Context, arg db.LockFriendshipParams) (db.LockFriendshipRow, error)
	InsertFriendRequest(ctx context.Context, arg db.InsertFriendRequestParams) error
	AcceptFriendship(ctx context.Context, arg db.AcceptFriendshipParams) error
	DeleteFriendship(ctx context.Context, arg db.DeleteFriendshipParams) (int64, error)
	CountRecentFriendRequests(ctx context.Context, arg db.CountRecentFriendRequestsParams) (int32, error)
	PersonExists(ctx context.Context, arg db.PersonExistsParams) (bool, error)
	IsBlockedEitherWay(ctx context.Context, arg db.IsBlockedEitherWayParams) (bool, error)
	AreFriends(ctx context.Context, arg db.AreFriendsParams) (bool, error)
	InsertBlock(ctx context.Context, arg db.InsertBlockParams) error
	DeleteBlock(ctx context.Context, arg db.DeleteBlockParams) (int64, error)
	DeleteLikesBetween(ctx context.Context, arg db.DeleteLikesBetweenParams) error
	DeleteNotificationsBetween(ctx context.Context, arg db.DeleteNotificationsBetweenParams) error
	WorkoutBelongsTo(ctx context.Context, arg db.WorkoutBelongsToParams) (bool, error)
	InsertLike(ctx context.Context, arg db.InsertLikeParams) (int64, error)
	DeleteLike(ctx context.Context, arg db.DeleteLikeParams) (int64, error)
	LikeSummary(ctx context.Context, arg db.LikeSummaryParams) (db.LikeSummaryRow, error)
	InsertNotification(ctx context.Context, arg db.InsertNotificationParams) error
	DeleteNotification(ctx context.Context, arg db.DeleteNotificationParams) error
	ListNotifications(ctx context.Context, arg db.ListNotificationsParams) ([]db.ListNotificationsRow, error)
	CountUnreadNotifications(ctx context.Context, userID uuid.UUID) (int32, error)
	MarkNotificationsRead(ctx context.Context, userID uuid.UUID) error
	SocialUserRole(ctx context.Context, id uuid.UUID) (db.UserRole, error)
	CountRecentReports(ctx context.Context, arg db.CountRecentReportsParams) (int32, error)
	OpenReportExists(ctx context.Context, arg db.OpenReportExistsParams) (bool, error)
	InsertReport(ctx context.Context, arg db.InsertReportParams) error
	ListReports(ctx context.Context, arg db.ListReportsParams) ([]db.ListReportsRow, error)
	ResolveReport(ctx context.Context, arg db.ResolveReportParams) (int64, error)
}

var _ Store = (*db.Queries)(nil)

// Service holds the social rules.
type Service struct {
	store Store
	// tx runs fn with a store bound to one transaction.
	tx func(ctx context.Context, fn func(Store) error) error
}

// NewService builds the service. inTx is db.InTx bound to the pool.
func NewService(store Store, inTx func(ctx context.Context, fn func(*db.Queries) error) error) *Service {
	return &Service{store: store, tx: func(ctx context.Context, fn func(Store) error) error {
		return inTx(ctx, func(q *db.Queries) error { return fn(q) })
	}}
}

// Person is someone as another person sees them in a list.
type Person struct {
	ID           uuid.UUID
	Username     string
	DisplayName  string
	AvatarURL    *string // friends and yourself only
	Relationship string
}

// Profile is one person's page.
type Profile struct {
	Person
	MemberSince time.Time
	// Workouts counts finished workouts.
	Workouts     int32
	FriendsSince *time.Time
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NotFound()
	}
	return err
}

func nu(id uuid.UUID) uuid.NullUUID { return uuid.NullUUID{UUID: id, Valid: true} }

// avatarFor is the picture a viewer may see: only a friend's (or their own).
func avatarFor(rel string, avatar *string) *string {
	if rel == RelFriends || rel == RelSelf {
		return avatar
	}
	return nil
}

// Search finds people whose username or name contains the text, or is close to it. Never the caller, never anyone
// blocked either way, and at least three characters so the list can't be walked a letter at a time.
func (s *Service) Search(ctx context.Context, viewer uuid.UUID, q string) ([]Person, error) {
	needle := strings.ToLower(strings.TrimSpace(q))
	n := utf8.RuneCountInString(needle)
	if n < minSearchChars {
		return nil, domain.Invalid("type at least %d characters to search", minSearchChars)
	}
	if n > maxSearchChars {
		needle = string([]rune(needle)[:maxSearchChars])
	}
	rows, err := s.store.SearchPeople(ctx, db.SearchPeopleParams{ViewerID: viewer, Needle: needle, MaxRows: searchRows})
	if err != nil {
		return nil, err
	}
	out := make([]Person, 0, len(rows))
	for _, r := range rows {
		out = append(out, Person{
			ID: r.ID, Username: r.Username, DisplayName: r.DisplayName, Relationship: r.Relationship,
			AvatarURL: avatarFor(r.Relationship, r.AvatarUrl),
		})
	}
	return out, nil
}

// Profile is one person's page as the caller may see it.
func (s *Service) Profile(ctx context.Context, viewer, id uuid.UUID) (Profile, error) {
	r, err := s.store.GetPerson(ctx, db.GetPersonParams{ViewerID: viewer, ID: id})
	if err != nil {
		return Profile{}, notFound(err)
	}
	rel := r.Relationship
	switch {
	case id == viewer:
		rel = RelSelf
	case r.BlockedByMe:
		rel = RelBlocked
	}
	p := Profile{
		Person: Person{
			ID: r.ID, Username: r.Username, DisplayName: r.DisplayName, Relationship: rel,
			AvatarURL: avatarFor(rel, r.AvatarUrl),
		},
		MemberSince: r.MemberSince, Workouts: r.Workouts,
	}
	if rel == RelFriends {
		p.FriendsSince = r.FriendsSince
	}
	return p, nil
}

// Friends lists the caller's friends.
func (s *Service) Friends(ctx context.Context, viewer uuid.UUID) ([]Person, error) {
	rows, err := s.store.ListFriends(ctx, viewer)
	if err != nil {
		return nil, err
	}
	out := make([]Person, 0, len(rows))
	for _, r := range rows {
		out = append(out, Person{ID: r.ID, Username: r.Username, DisplayName: r.DisplayName, AvatarURL: r.AvatarUrl, Relationship: RelFriends})
	}
	return out, nil
}

// Requests lists the friend requests waiting for the caller and the ones the caller sent.
func (s *Service) Requests(ctx context.Context, viewer uuid.UUID) (incoming, outgoing []Person, err error) {
	in, err := s.store.ListIncomingRequests(ctx, viewer)
	if err != nil {
		return nil, nil, err
	}
	out, err := s.store.ListOutgoingRequests(ctx, viewer)
	if err != nil {
		return nil, nil, err
	}
	incoming, outgoing = make([]Person, 0, len(in)), make([]Person, 0, len(out))
	for _, r := range in {
		incoming = append(incoming, Person{ID: r.ID, Username: r.Username, DisplayName: r.DisplayName, Relationship: RelRequestReceived})
	}
	for _, r := range out {
		outgoing = append(outgoing, Person{ID: r.ID, Username: r.Username, DisplayName: r.DisplayName, Relationship: RelRequestSent})
	}
	return incoming, outgoing, nil
}

// Blocked lists the people the caller has blocked, so they can be unblocked.
func (s *Service) Blocked(ctx context.Context, viewer uuid.UUID) ([]Person, error) {
	rows, err := s.store.ListBlocked(ctx, viewer)
	if err != nil {
		return nil, err
	}
	out := make([]Person, 0, len(rows))
	for _, r := range rows {
		out = append(out, Person{ID: r.ID, Username: r.Username, DisplayName: r.DisplayName, Relationship: RelBlocked})
	}
	return out, nil
}

// visible fails with not found unless the other account exists and has not blocked the caller.
func visible(ctx context.Context, st Store, viewer, other uuid.UUID) error {
	found, err := st.PersonExists(ctx, db.PersonExistsParams{ID: other, ViewerID: viewer})
	if err != nil {
		return err
	}
	if !found {
		return domain.NotFound()
	}
	return nil
}

// SendRequest asks someone to be friends. If they had already asked the caller, this accepts that request. Asking
// again changes nothing. Returns the new relationship.
func (s *Service) SendRequest(ctx context.Context, viewer, target uuid.UUID) (string, error) {
	if viewer == target {
		return "", domain.Invalid("you can't add yourself")
	}
	rel := RelNone
	err := s.tx(ctx, func(st Store) error {
		if err := visible(ctx, st, viewer, target); err != nil {
			return err
		}
		blocked, err := st.IsBlockedEitherWay(ctx, db.IsBlockedEitherWayParams{A: viewer, B: target})
		if err != nil {
			return err
		}
		if blocked { // visible() already ruled out the other direction
			return domain.Conflict("blocked", "you blocked this person; unblock them first")
		}
		row, err := st.LockFriendship(ctx, db.LockFriendshipParams{A: viewer, B: target})
		switch {
		case err == nil && row.Status == "accepted":
			rel = RelFriends
			return nil
		case err == nil && row.RequesterID == viewer:
			rel = RelRequestSent
			return nil
		case err == nil: // they asked first: this is a yes
			rel = RelFriends
			return accept(ctx, st, viewer, target)
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		n, err := st.CountRecentFriendRequests(ctx, db.CountRecentFriendRequestsParams{UserID: viewer, Since: time.Now().Add(-rateWindow)})
		if err != nil {
			return err
		}
		if n >= maxRequestsPerHour {
			return domain.RateLimited("too many friend requests, try again later")
		}
		if err := st.InsertFriendRequest(ctx, db.InsertFriendRequestParams{RequesterID: viewer, AddresseeID: target}); err != nil {
			return err
		}
		rel = RelRequestSent
		return st.InsertNotification(ctx, db.InsertNotificationParams{UserID: target, Kind: NoteFriendRequest, ActorID: viewer})
	})
	return rel, err
}

// accept turns the pending request from `from` to `viewer` into a friendship and tells `from`.
func accept(ctx context.Context, st Store, viewer, from uuid.UUID) error {
	if err := st.AcceptFriendship(ctx, db.AcceptFriendshipParams{RequesterID: from, AddresseeID: viewer}); err != nil {
		return err
	}
	if err := st.DeleteNotification(ctx, db.DeleteNotificationParams{UserID: viewer, Kind: NoteFriendRequest, ActorID: from}); err != nil {
		return err
	}
	return st.InsertNotification(ctx, db.InsertNotificationParams{UserID: from, Kind: NoteFriendAccepted, ActorID: viewer})
}

// Accept says yes to the request `from` sent the caller.
func (s *Service) Accept(ctx context.Context, viewer, from uuid.UUID) (string, error) {
	err := s.tx(ctx, func(st Store) error {
		if err := visible(ctx, st, viewer, from); err != nil {
			return err
		}
		row, err := st.LockFriendship(ctx, db.LockFriendshipParams{A: viewer, B: from})
		if err != nil {
			return notFound(err)
		}
		if row.Status != "pending" || row.RequesterID != from {
			return domain.NotFound()
		}
		return accept(ctx, st, viewer, from)
	})
	if err != nil {
		return "", err
	}
	return RelFriends, nil
}

// Remove cancels a request the caller sent, declines one they received, or unfriends: whichever applies.
func (s *Service) Remove(ctx context.Context, viewer, other uuid.UUID) (string, error) {
	err := s.tx(ctx, func(st Store) error {
		n, err := st.DeleteFriendship(ctx, db.DeleteFriendshipParams{A: viewer, B: other})
		if err != nil {
			return err
		}
		if n == 0 {
			return domain.NotFound()
		}
		// A request that no longer exists should not linger in anyone's notifications.
		for _, p := range [][2]uuid.UUID{{viewer, other}, {other, viewer}} {
			if err := st.DeleteNotification(ctx, db.DeleteNotificationParams{UserID: p[0], Kind: NoteFriendRequest, ActorID: p[1]}); err != nil {
				return err
			}
		}
		return nil
	})
	return RelNone, err
}

// Block hides the caller and the other person from each other and ends any friendship between them.
func (s *Service) Block(ctx context.Context, viewer, target uuid.UUID) error {
	if viewer == target {
		return domain.Invalid("you can't block yourself")
	}
	return s.tx(ctx, func(st Store) error {
		if err := visible(ctx, st, viewer, target); err != nil {
			return err
		}
		if err := st.InsertBlock(ctx, db.InsertBlockParams{BlockerID: viewer, BlockedID: target}); err != nil {
			return err
		}
		if _, err := st.DeleteFriendship(ctx, db.DeleteFriendshipParams{A: viewer, B: target}); err != nil {
			return err
		}
		if err := st.DeleteLikesBetween(ctx, db.DeleteLikesBetweenParams{A: viewer, B: target}); err != nil {
			return err
		}
		return st.DeleteNotificationsBetween(ctx, db.DeleteNotificationsBetweenParams{A: viewer, B: target})
	})
}

// Unblock lifts a block. The friendship does not come back.
func (s *Service) Unblock(ctx context.Context, viewer, target uuid.UUID) error {
	n, err := s.store.DeleteBlock(ctx, db.DeleteBlockParams{BlockerID: viewer, BlockedID: target})
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.NotFound()
	}
	return nil
}

// CanView is nil when the caller may see `owner`'s workouts: they are friends and neither has blocked the other.
// Anything else is not found, so a stranger learns nothing about whether the person has workouts.
func (s *Service) CanView(ctx context.Context, viewer, owner uuid.UUID) error {
	return canView(ctx, s.store, viewer, owner)
}

func canView(ctx context.Context, st Store, viewer, owner uuid.UUID) error {
	if viewer == owner {
		return nil
	}
	if err := visible(ctx, st, viewer, owner); err != nil {
		return err
	}
	blocked, err := st.IsBlockedEitherWay(ctx, db.IsBlockedEitherWayParams{A: viewer, B: owner})
	if err != nil {
		return err
	}
	if blocked {
		return domain.NotFound()
	}
	friends, err := st.AreFriends(ctx, db.AreFriendsParams{A: viewer, B: owner})
	if err != nil {
		return err
	}
	if !friends {
		return domain.NotFound()
	}
	return nil
}

// Likes is how many people liked a workout and whether the caller did.
type Likes struct {
	Count int32
	Liked bool
}

// LikesOf reads a friend's workout's likes. The caller must be allowed to see the workout.
func (s *Service) LikesOf(ctx context.Context, viewer, owner, workout uuid.UUID) (Likes, error) {
	if err := canView(ctx, s.store, viewer, owner); err != nil {
		return Likes{}, err
	}
	return likes(ctx, s.store, viewer, workout)
}

func likes(ctx context.Context, st Store, viewer, workout uuid.UUID) (Likes, error) {
	r, err := st.LikeSummary(ctx, db.LikeSummaryParams{ViewerID: viewer, WorkoutID: workout})
	return Likes{Count: r.Likes, Liked: r.Liked}, err
}

// Like likes a friend's workout and tells its owner. Liking twice is one like.
func (s *Service) Like(ctx context.Context, viewer, owner, workout uuid.UUID) (Likes, error) {
	if viewer == owner {
		return Likes{}, domain.Invalid("you can't like your own workout")
	}
	var out Likes
	err := s.tx(ctx, func(st Store) error {
		if err := canView(ctx, st, viewer, owner); err != nil {
			return err
		}
		mine, err := st.WorkoutBelongsTo(ctx, db.WorkoutBelongsToParams{ID: workout, OwnerID: owner})
		if err != nil {
			return err
		}
		if !mine {
			return domain.NotFound()
		}
		n, err := st.InsertLike(ctx, db.InsertLikeParams{WorkoutID: workout, UserID: viewer})
		if err != nil {
			return err
		}
		if n > 0 {
			if err := st.InsertNotification(ctx, db.InsertNotificationParams{UserID: owner, Kind: NoteWorkoutLiked, ActorID: viewer, WorkoutID: nu(workout)}); err != nil {
				return err
			}
		}
		out, err = likes(ctx, st, viewer, workout)
		return err
	})
	return out, err
}

// Unlike takes a like back, and the notification with it.
func (s *Service) Unlike(ctx context.Context, viewer, owner, workout uuid.UUID) (Likes, error) {
	var out Likes
	err := s.tx(ctx, func(st Store) error {
		if err := canView(ctx, st, viewer, owner); err != nil {
			return err
		}
		if _, err := st.DeleteLike(ctx, db.DeleteLikeParams{WorkoutID: workout, UserID: viewer}); err != nil {
			return err
		}
		if err := st.DeleteNotification(ctx, db.DeleteNotificationParams{UserID: owner, Kind: NoteWorkoutLiked, ActorID: viewer, WorkoutID: nu(workout)}); err != nil {
			return err
		}
		var err error
		out, err = likes(ctx, st, viewer, workout)
		return err
	})
	return out, err
}

// Notification is one thing that happened that the caller may want to know about.
type Notification struct {
	ID        uuid.UUID
	Kind      string
	ActorID   uuid.UUID
	ActorName string
	ActorUser string
	WorkoutID *uuid.UUID
	At        time.Time
	Read      bool
}

// Notifications lists the caller's notifications, newest first, with how many are unread.
func (s *Service) Notifications(ctx context.Context, user uuid.UUID, limit *int64) (items []Notification, unread int32, err error) {
	n := int64(defaultNotifRows)
	if limit != nil {
		n = min(max(*limit, 1), maxNotifRows)
	}
	rows, err := s.store.ListNotifications(ctx, db.ListNotificationsParams{UserID: user, MaxRows: int32(n)}) //nolint:gosec // clamped to 1..100
	if err != nil {
		return nil, 0, err
	}
	unread, err = s.store.CountUnreadNotifications(ctx, user)
	if err != nil {
		return nil, 0, err
	}
	items = make([]Notification, 0, len(rows))
	for _, r := range rows {
		it := Notification{ID: r.ID, Kind: r.Kind, ActorID: r.ActorID, ActorName: r.ActorName, ActorUser: r.ActorUsername, At: r.CreatedAt, Read: r.Read}
		if r.WorkoutID.Valid {
			id := r.WorkoutID.UUID
			it.WorkoutID = &id
		}
		items = append(items, it)
	}
	return items, unread, nil
}

// UnreadCount is the badge number.
func (s *Service) UnreadCount(ctx context.Context, user uuid.UUID) (int32, error) {
	return s.store.CountUnreadNotifications(ctx, user)
}

// MarkRead marks everything read.
func (s *Service) MarkRead(ctx context.Context, user uuid.UUID) error {
	return s.store.MarkNotificationsRead(ctx, user)
}

// ReportInput is a report of a person, or of one of their workouts.
type ReportInput struct {
	UserID    uuid.UUID
	WorkoutID *uuid.UUID
	Reason    string
}

// Report tells the moderators about a person or a workout. Reporting the same thing again while the first report is
// open changes nothing. A workout can be reported only by someone who may see it.
func (s *Service) Report(ctx context.Context, reporter uuid.UUID, in ReportInput) error {
	reason := strings.TrimSpace(in.Reason)
	if n := utf8.RuneCountInString(reason); n < 1 || n > maxReasonChars {
		return domain.Invalid("reason must be 1-%d characters", maxReasonChars)
	}
	if in.UserID == reporter {
		return domain.Invalid("you can't report yourself")
	}
	return s.tx(ctx, func(st Store) error {
		if err := visible(ctx, st, reporter, in.UserID); err != nil {
			return err
		}
		wid := uuid.NullUUID{}
		if in.WorkoutID != nil {
			if err := canView(ctx, st, reporter, in.UserID); err != nil {
				return err
			}
			mine, err := st.WorkoutBelongsTo(ctx, db.WorkoutBelongsToParams{ID: *in.WorkoutID, OwnerID: in.UserID})
			if err != nil {
				return err
			}
			if !mine {
				return domain.NotFound()
			}
			wid = nu(*in.WorkoutID)
		}
		dup, err := st.OpenReportExists(ctx, db.OpenReportExistsParams{ReporterID: nu(reporter), TargetUserID: in.UserID, WorkoutID: wid})
		if err != nil || dup {
			return err
		}
		n, err := st.CountRecentReports(ctx, db.CountRecentReportsParams{UserID: nu(reporter), Since: time.Now().Add(-rateWindow)})
		if err != nil {
			return err
		}
		if n >= maxReportsPerHour {
			return domain.RateLimited("too many reports, try again later")
		}
		return st.InsertReport(ctx, db.InsertReportParams{ReporterID: nu(reporter), TargetUserID: in.UserID, WorkoutID: wid, Reason: reason})
	})
}

func staff(ctx context.Context, st Store, user uuid.UUID) error {
	role, err := st.SocialUserRole(ctx, user)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Unauthorized()
	}
	if err != nil {
		return err
	}
	if role != db.UserRoleModerator && role != db.UserRoleAdmin {
		return domain.Forbidden("forbidden", "only moderators can work the reports")
	}
	return nil
}

// Report statuses.
const (
	ReportOpen     = "open"
	ReportResolved = "resolved"
)

// ReportItem is a report as a moderator sees it.
type ReportItem struct {
	ID               uuid.UUID
	Status           string
	Reason           string
	At               time.Time
	WorkoutID        *uuid.UUID
	ReporterID       *uuid.UUID
	ReporterUsername *string
	TargetID         uuid.UUID
	TargetUsername   string
	TargetName       string
}

// Reports lists reports with the given status (open by default), newest first. Moderators and admins only.
func (s *Service) Reports(ctx context.Context, user uuid.UUID, status string) ([]ReportItem, error) {
	if status == "" {
		status = ReportOpen
	}
	if status != ReportOpen && status != ReportResolved {
		return nil, domain.Invalid("status must be open or resolved")
	}
	if err := staff(ctx, s.store, user); err != nil {
		return nil, err
	}
	rows, err := s.store.ListReports(ctx, db.ListReportsParams{Status: status, MaxRows: maxReportRows})
	if err != nil {
		return nil, err
	}
	out := make([]ReportItem, 0, len(rows))
	for _, r := range rows {
		it := ReportItem{
			ID: r.ID, Status: r.Status, Reason: r.Reason, At: r.CreatedAt,
			TargetID: r.TargetID, TargetUsername: r.TargetUsername, TargetName: r.TargetName,
		}
		if r.WorkoutID.Valid {
			id := r.WorkoutID.UUID
			it.WorkoutID = &id
		}
		if r.ReporterID.Valid {
			id := r.ReporterID.UUID
			it.ReporterID = &id
			name := r.ReporterUsername
			it.ReporterUsername = &name
		}
		out = append(out, it)
	}
	return out, nil
}

// ResolveReport marks a report handled. Moderators and admins only.
func (s *Service) ResolveReport(ctx context.Context, user, id uuid.UUID) error {
	if err := staff(ctx, s.store, user); err != nil {
		return err
	}
	n, err := s.store.ResolveReport(ctx, db.ResolveReportParams{ID: id, By: nu(user)})
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.NotFound()
	}
	return nil
}
