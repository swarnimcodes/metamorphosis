package httpapi

import (
	"context"

	"github.com/google/uuid"

	"github.com/smarniw/metamorphosis/internal/db"
	"github.com/smarniw/metamorphosis/internal/httpapi/gen"
	"github.com/smarniw/metamorphosis/internal/social"
	"github.com/smarniw/metamorphosis/internal/stats"
	"github.com/smarniw/metamorphosis/internal/workout"
)

// socialAPI holds the handlers of the social operations in api/openapi.yaml. A friend's workouts are read through
// the same workout service as one's own, after the social service has said the caller may see them.
type socialAPI struct {
	soc      *social.Service
	workouts *workout.Service
	stats    *stats.Service
}

func newSocialAPI(d Deps, q *db.Queries) socialAPI {
	inTx := func(ctx context.Context, fn func(*db.Queries) error) error { return db.InTx(ctx, d.DB, fn) }
	return socialAPI{soc: social.NewService(q, inTx), workouts: workout.NewService(q, inTx), stats: stats.NewService(q)}
}

func personOut(p social.Person) gen.Person {
	return gen.Person{
		Id: p.ID, Username: p.Username, DisplayName: p.DisplayName, AvatarUrl: p.AvatarURL,
		Relationship: gen.PersonRelationship(p.Relationship),
	}
}

func peopleOut(in []social.Person) []gen.Person {
	out := make([]gen.Person, 0, len(in))
	for _, p := range in {
		out = append(out, personOut(p))
	}
	return out
}

func relationshipOut(rel string) gen.RelationshipState {
	return gen.RelationshipState{Relationship: gen.RelationshipStateRelationship(rel)}
}

func (a socialAPI) SearchPeople(ctx context.Context, req gen.SearchPeopleRequestObject) (gen.SearchPeopleResponseObject, error) {
	found, err := a.soc.Search(ctx, userFrom(ctx), req.Params.Q)
	if err != nil {
		return nil, err
	}
	return gen.SearchPeople200JSONResponse(peopleOut(found)), nil
}

func (a socialAPI) GetPerson(ctx context.Context, req gen.GetPersonRequestObject) (gen.GetPersonResponseObject, error) {
	p, err := a.soc.Profile(ctx, userFrom(ctx), req.Id)
	if err != nil {
		return nil, err
	}
	return gen.GetPerson200JSONResponse{
		Id: p.ID, Username: p.Username, DisplayName: p.DisplayName, AvatarUrl: p.AvatarURL,
		Relationship: gen.PersonProfileRelationship(p.Relationship), MemberSince: p.MemberSince,
		Workouts: p.Workouts, FriendsSince: p.FriendsSince,
	}, nil
}

func (a socialAPI) ListPersonWorkouts(ctx context.Context, req gen.ListPersonWorkoutsRequestObject) (gen.ListPersonWorkoutsResponseObject, error) {
	if err := a.soc.CanView(ctx, userFrom(ctx), req.Id); err != nil {
		return nil, err
	}
	rows, err := a.workouts.List(ctx, req.Id, req.Params.Limit, req.Params.Before)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListPersonWorkouts200JSONResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, gen.WorkoutSummary{
			Id: r.ID, Title: r.Title, GymId: r.GymID, Gym: workoutGym(r.Gym), StartedAt: r.StartedAt, EndedAt: r.EndedAt,
			Timezone: r.Timezone, ExerciseCount: r.ExerciseCount, SetCount: r.SetCount,
		})
	}
	return out, nil
}

// withoutNotes is a workout as a friend may read it: the free-text notes stay with their author.
func withoutNotes(w workout.Workout) workout.Workout {
	w.Notes = nil
	exs := make([]workout.Exercise, len(w.Exercises))
	for i, e := range w.Exercises {
		e.Notes = nil
		sets := make([]workout.Set, len(e.Sets))
		for j, s := range e.Sets {
			s.Note = nil
			sets[j] = s
		}
		e.Sets = sets
		exs[i] = e
	}
	w.Exercises = exs
	return w
}

func (a socialAPI) GetPersonWorkout(ctx context.Context, req gen.GetPersonWorkoutRequestObject) (gen.GetPersonWorkoutResponseObject, error) {
	me := userFrom(ctx)
	if err := a.soc.CanView(ctx, me, req.Id); err != nil {
		return nil, err
	}
	w, err := a.workouts.Get(ctx, req.Id, req.WorkoutId)
	if err != nil {
		return nil, err
	}
	l, err := a.soc.LikesOf(ctx, me, req.Id, req.WorkoutId)
	if err != nil {
		return nil, err
	}
	return gen.GetPersonWorkout200JSONResponse{Workout: workoutFull(withoutNotes(w)), Likes: l.Count, Liked: l.Liked}, nil
}

func (a socialAPI) LikePersonWorkout(ctx context.Context, req gen.LikePersonWorkoutRequestObject) (gen.LikePersonWorkoutResponseObject, error) {
	l, err := a.soc.Like(ctx, userFrom(ctx), req.Id, req.WorkoutId)
	if err != nil {
		return nil, err
	}
	return gen.LikePersonWorkout200JSONResponse{Likes: l.Count, Liked: l.Liked}, nil
}

func (a socialAPI) UnlikePersonWorkout(ctx context.Context, req gen.UnlikePersonWorkoutRequestObject) (gen.UnlikePersonWorkoutResponseObject, error) {
	l, err := a.soc.Unlike(ctx, userFrom(ctx), req.Id, req.WorkoutId)
	if err != nil {
		return nil, err
	}
	return gen.UnlikePersonWorkout200JSONResponse{Likes: l.Count, Liked: l.Liked}, nil
}

func (a socialAPI) SendFriendRequest(ctx context.Context, req gen.SendFriendRequestRequestObject) (gen.SendFriendRequestResponseObject, error) {
	rel, err := a.soc.SendRequest(ctx, userFrom(ctx), req.Id)
	if err != nil {
		return nil, err
	}
	return gen.SendFriendRequest200JSONResponse(relationshipOut(rel)), nil
}

func (a socialAPI) AcceptFriendRequest(ctx context.Context, req gen.AcceptFriendRequestRequestObject) (gen.AcceptFriendRequestResponseObject, error) {
	rel, err := a.soc.Accept(ctx, userFrom(ctx), req.Id)
	if err != nil {
		return nil, err
	}
	return gen.AcceptFriendRequest200JSONResponse(relationshipOut(rel)), nil
}

func (a socialAPI) RemoveFriendship(ctx context.Context, req gen.RemoveFriendshipRequestObject) (gen.RemoveFriendshipResponseObject, error) {
	rel, err := a.soc.Remove(ctx, userFrom(ctx), req.Id, req.Params.PendingOnly != nil && *req.Params.PendingOnly)
	if err != nil {
		return nil, err
	}
	return gen.RemoveFriendship200JSONResponse(relationshipOut(rel)), nil
}

func (a socialAPI) BlockPerson(ctx context.Context, req gen.BlockPersonRequestObject) (gen.BlockPersonResponseObject, error) {
	if err := a.soc.Block(ctx, userFrom(ctx), req.Id); err != nil {
		return nil, err
	}
	return gen.BlockPerson204Response{}, nil
}

func (a socialAPI) UnblockPerson(ctx context.Context, req gen.UnblockPersonRequestObject) (gen.UnblockPersonResponseObject, error) {
	if err := a.soc.Unblock(ctx, userFrom(ctx), req.Id); err != nil {
		return nil, err
	}
	return gen.UnblockPerson204Response{}, nil
}

func (a socialAPI) ListFriends(ctx context.Context, _ gen.ListFriendsRequestObject) (gen.ListFriendsResponseObject, error) {
	friends, err := a.soc.Friends(ctx, userFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.ListFriends200JSONResponse(peopleOut(friends)), nil
}

func (a socialAPI) ListFriendRequests(ctx context.Context, _ gen.ListFriendRequestsRequestObject) (gen.ListFriendRequestsResponseObject, error) {
	in, out, err := a.soc.Requests(ctx, userFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.ListFriendRequests200JSONResponse{Incoming: peopleOut(in), Outgoing: peopleOut(out)}, nil
}

func (a socialAPI) ListBlocks(ctx context.Context, _ gen.ListBlocksRequestObject) (gen.ListBlocksResponseObject, error) {
	blocked, err := a.soc.Blocked(ctx, userFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.ListBlocks200JSONResponse(peopleOut(blocked)), nil
}

func (a socialAPI) ListNotifications(ctx context.Context, req gen.ListNotificationsRequestObject) (gen.ListNotificationsResponseObject, error) {
	items, unread, err := a.soc.Notifications(ctx, userFrom(ctx), req.Params.Limit)
	if err != nil {
		return nil, err
	}
	out := make([]gen.Notification, 0, len(items))
	for _, n := range items {
		out = append(out, gen.Notification{
			Id: n.ID, Kind: gen.NotificationKind(n.Kind), At: n.At, Read: n.Read, WorkoutId: n.WorkoutID,
			Actor: gen.NotificationActor{Id: n.ActorID, Username: n.ActorUser, DisplayName: n.ActorName},
		})
	}
	return gen.ListNotifications200JSONResponse{Unread: unread, Items: out}, nil
}

func (a socialAPI) GetUnreadNotifications(ctx context.Context, _ gen.GetUnreadNotificationsRequestObject) (gen.GetUnreadNotificationsResponseObject, error) {
	n, err := a.soc.UnreadCount(ctx, userFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.GetUnreadNotifications200JSONResponse{Count: n}, nil
}

func (a socialAPI) MarkNotificationsRead(ctx context.Context, _ gen.MarkNotificationsReadRequestObject) (gen.MarkNotificationsReadResponseObject, error) {
	if err := a.soc.MarkRead(ctx, userFrom(ctx)); err != nil {
		return nil, err
	}
	return gen.MarkNotificationsRead204Response{}, nil
}

func (a socialAPI) CreateReport(ctx context.Context, req gen.CreateReportRequestObject) (gen.CreateReportResponseObject, error) {
	b := req.Body
	var wid *uuid.UUID
	if b.WorkoutId != nil {
		wid = b.WorkoutId
	}
	if err := a.soc.Report(ctx, userFrom(ctx), social.ReportInput{UserID: b.UserId, WorkoutID: wid, Reason: b.Reason}); err != nil {
		return nil, err
	}
	return gen.CreateReport204Response{}, nil
}

func (a socialAPI) ListReports(ctx context.Context, req gen.ListReportsRequestObject) (gen.ListReportsResponseObject, error) {
	status := ""
	if req.Params.Status != nil {
		status = string(*req.Params.Status)
	}
	items, err := a.soc.Reports(ctx, userFrom(ctx), status)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListReports200JSONResponse, 0, len(items))
	for _, r := range items {
		item := gen.Report{
			Id: r.ID, Status: gen.ReportStatus(r.Status), Reason: r.Reason, At: r.At, WorkoutId: r.WorkoutID,
			Target: gen.ReportPerson{Id: r.TargetID, Username: r.TargetUsername, DisplayName: r.TargetName},
		}
		if r.ReporterID != nil && r.ReporterUsername != nil {
			item.Reporter = &gen.ReportPerson{Id: *r.ReporterID, Username: *r.ReporterUsername, DisplayName: *r.ReporterUsername}
		}
		out = append(out, item)
	}
	return out, nil
}

func (a socialAPI) ResolveReport(ctx context.Context, req gen.ResolveReportRequestObject) (gen.ResolveReportResponseObject, error) {
	if err := a.soc.ResolveReport(ctx, userFrom(ctx), req.Id); err != nil {
		return nil, err
	}
	return gen.ResolveReport204Response{}, nil
}

func (a socialAPI) GetPersonConsistency(ctx context.Context, req gen.GetPersonConsistencyRequestObject) (gen.GetPersonConsistencyResponseObject, error) {
	if err := a.soc.CanView(ctx, userFrom(ctx), req.Id); err != nil {
		return nil, err
	}
	weeks := int64(stats.DefaultWeeks)
	if req.Params.Weeks != nil {
		weeks = *req.Params.Weeks
	}
	c, err := a.stats.Consistency(ctx, req.Id, weeks)
	if err != nil {
		return nil, err
	}
	return gen.GetPersonConsistency200JSONResponse(consistencyOut(c)), nil
}
