-- Standard gym machines the starter catalog was missing.
insert into muscle_groups (slug, name) values ('adductors', 'Adductors')
on conflict (slug) do nothing;

insert into exercises (slug, name, kind, status) values
    ('hip-abduction', 'Hip Abduction', 'weight_reps', 'official'),
    ('hip-adduction', 'Hip Adduction', 'weight_reps', 'official')
on conflict (slug) do nothing;

insert into exercise_muscles (exercise_id, muscle_group_id, role)
select e.id, m.id, v.role::muscle_role
  from (values
    ('hip-abduction', 'glutes', 'primary'),
    ('hip-adduction', 'adductors', 'primary')
  ) as v (exercise_slug, muscle_slug, role)
  join exercises e on e.slug = v.exercise_slug
  join muscle_groups m on m.slug = v.muscle_slug
on conflict do nothing;

insert into exercise_aliases (exercise_id, alias)
select e.id, v.alias
  from (values
    ('hip-abduction', 'Abduction'), ('hip-abduction', 'Abductor'), ('hip-abduction', 'Hip Abductor'),
    ('hip-adduction', 'Adduction'), ('hip-adduction', 'Adductor'), ('hip-adduction', 'Hip Adductor')
  ) as v (exercise_slug, alias)
  join exercises e on e.slug = v.exercise_slug
on conflict do nothing;

insert into exercise_variation_tags (exercise_id, tag_id)
select e.id, t.id
  from exercises e, variation_tags t
 where e.slug in ('hip-abduction', 'hip-adduction') and t.slug in ('machine', 'seated')
on conflict do nothing;
