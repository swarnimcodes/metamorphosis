-- Preacher curls are Bicep Curl + the preacher bench, not a separate exercise.
insert into variation_tags (slug, name, category) values ('preacher', 'Preacher', 'equipment')
on conflict (slug) do nothing;

insert into exercise_variation_tags (exercise_id, tag_id)
select e.id, t.id
  from exercises e, variation_tags t
 where e.slug = 'bicep-curl' and t.slug = 'preacher'
on conflict do nothing;
