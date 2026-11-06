-- name: SearchExercises :many
-- Matches canonical names and aliases. The query is normalised the same way as exercise_aliases.alias_normalized.
-- Official exercises and the caller's own are visible; merged and deleted ones are not.
with q as (
    select btrim(lower(regexp_replace(@q::text, '[^[:alnum:]]+', ' ', 'g'))) as nq
), cand as (
    select e.id, e.slug, e.name, e.kind, e.status,
           btrim(lower(regexp_replace(e.name, '[^[:alnum:]]+', ' ', 'g'))) as nname
      from exercises e
     where e.deleted_at is null
       and e.merged_into_id is null
       and (e.status = 'official' or e.created_by = @user_id)
), scored as (
    select c.*,
           greatest(
               similarity(c.nname, q.nq),
               coalesce((select max(similarity(a.alias_normalized, q.nq))
                           from exercise_aliases a where a.exercise_id = c.id), 0)
           ) as score,
           (c.nname like '%' || q.nq || '%'
            or exists (select 1 from exercise_aliases a
                        where a.exercise_id = c.id
                          and a.alias_normalized like '%' || q.nq || '%')) as contains,
           (c.nname = q.nq
            or exists (select 1 from exercise_aliases a
                        where a.exercise_id = c.id
                          and a.alias_normalized = q.nq)) as exact
      from cand c, q
)
select id, slug, name, kind::text as kind, status::text as status, coalesce(exact, false) as exact
  from scored, q
 where q.nq = '' or contains or score > 0.3
 order by exact desc,
          (case when q.nq = '' then 0 when contains then 1 else 0 end) desc,
          score desc, name
 limit @max_rows;

-- name: ExercisesSuggestingTags :many
-- When a query is only tag words ("preacher"): the exercises the catalog lists those tags for.
select distinct e.id, e.slug, e.name, e.kind::text as kind, e.status::text as status, false as exact
  from exercises e
  join exercise_variation_tags evt on evt.exercise_id = e.id
 where evt.tag_id = any(@tag_ids::uuid[])
   and e.deleted_at is null and e.merged_into_id is null
   and (e.status = 'official' or e.created_by = @user_id)
 order by e.name
 limit @max_rows;

-- name: ExerciseTagLinks :many
select exercise_id, tag_id from exercise_variation_tags
 where exercise_id = any(@exercise_ids::uuid[]) and tag_id = any(@tag_ids::uuid[]);

-- name: TagNames :many
select id, name from variation_tags;

-- name: ListTags :many
select id, slug, name, category::text as category from variation_tags order by variation_tags.category, name;
