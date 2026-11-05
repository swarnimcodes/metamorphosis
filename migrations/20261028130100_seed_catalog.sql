-- Reference data: muscle groups, variation tags and a starter set of official
-- exercises. Everything is keyed by slug so rows can be looked up without
-- knowing generated ids. Variations (equipment, grip, angle) are tags, not
-- separate exercises, so "Incline Dumbbell Bench Press" is Bench Press + incline + dumbbell.

insert into muscle_groups (slug, name) values
    ('chest', 'Chest'),
    ('front-delts', 'Front Delts'),
    ('side-delts', 'Side Delts'),
    ('rear-delts', 'Rear Delts'),
    ('lats', 'Lats'),
    ('upper-back', 'Upper Back'),
    ('traps', 'Traps'),
    ('biceps', 'Biceps'),
    ('triceps', 'Triceps'),
    ('forearms', 'Forearms'),
    ('quads', 'Quads'),
    ('hamstrings', 'Hamstrings'),
    ('glutes', 'Glutes'),
    ('calves', 'Calves'),
    ('abs', 'Abs'),
    ('lower-back', 'Lower Back')
on conflict (slug) do nothing;

insert into variation_tags (slug, name, category) values
    ('barbell', 'Barbell', 'equipment'),
    ('dumbbell', 'Dumbbell', 'equipment'),
    ('cable', 'Cable', 'equipment'),
    ('machine', 'Machine', 'equipment'),
    ('smith-machine', 'Smith Machine', 'equipment'),
    ('ez-bar', 'EZ Bar', 'equipment'),
    ('straight-bar', 'Straight Bar', 'equipment'),
    ('v-bar', 'V-Bar', 'equipment'),
    ('rope', 'Rope', 'equipment'),
    ('resistance-band', 'Resistance Band', 'equipment'),
    ('kettlebell', 'Kettlebell', 'equipment'),
    ('wide-grip', 'Wide Grip', 'grip'),
    ('close-grip', 'Close Grip', 'grip'),
    ('neutral-grip', 'Neutral Grip', 'grip'),
    ('overhand-grip', 'Overhand Grip', 'grip'),
    ('underhand-grip', 'Underhand Grip', 'grip'),
    ('incline', 'Incline', 'angle'),
    ('decline', 'Decline', 'angle'),
    ('flat', 'Flat', 'angle'),
    ('seated', 'Seated', 'stance'),
    ('standing', 'Standing', 'stance'),
    ('lying', 'Lying', 'stance'),
    ('single-arm', 'Single Arm', 'stance'),
    ('single-leg', 'Single Leg', 'stance'),
    ('overhead', 'Overhead', 'other'),
    ('paused', 'Paused', 'other')
on conflict (slug) do nothing;

insert into exercises (slug, name, kind, status) values
    ('bench-press', 'Bench Press', 'weight_reps', 'official'),
    ('chest-fly', 'Chest Fly', 'weight_reps', 'official'),
    ('push-up', 'Push-Up', 'bodyweight_reps', 'official'),
    ('dip', 'Dip', 'bodyweight_reps', 'official'),
    ('lat-pulldown', 'Lat Pulldown', 'weight_reps', 'official'),
    ('pull-up', 'Pull-Up', 'bodyweight_reps', 'official'),
    ('row', 'Row', 'weight_reps', 'official'),
    ('deadlift', 'Deadlift', 'weight_reps', 'official'),
    ('overhead-press', 'Overhead Press', 'weight_reps', 'official'),
    ('lateral-raise', 'Lateral Raise', 'weight_reps', 'official'),
    ('rear-delt-fly', 'Rear Delt Fly', 'weight_reps', 'official'),
    ('face-pull', 'Face Pull', 'weight_reps', 'official'),
    ('shrug', 'Shrug', 'weight_reps', 'official'),
    ('bicep-curl', 'Bicep Curl', 'weight_reps', 'official'),
    ('hammer-curl', 'Hammer Curl', 'weight_reps', 'official'),
    ('tricep-extension', 'Tricep Extension', 'weight_reps', 'official'),
    ('tricep-pushdown', 'Tricep Pushdown', 'weight_reps', 'official'),
    ('wrist-curl', 'Wrist Curl', 'weight_reps', 'official'),
    ('squat', 'Squat', 'weight_reps', 'official'),
    ('leg-press', 'Leg Press', 'weight_reps', 'official'),
    ('lunge', 'Lunge', 'weight_reps', 'official'),
    ('romanian-deadlift', 'Romanian Deadlift', 'weight_reps', 'official'),
    ('leg-extension', 'Leg Extension', 'weight_reps', 'official'),
    ('leg-curl', 'Leg Curl', 'weight_reps', 'official'),
    ('hip-thrust', 'Hip Thrust', 'weight_reps', 'official'),
    ('calf-raise', 'Calf Raise', 'weight_reps', 'official'),
    ('plank', 'Plank', 'duration', 'official'),
    ('crunch', 'Crunch', 'bodyweight_reps', 'official'),
    ('leg-raise', 'Leg Raise', 'bodyweight_reps', 'official'),
    ('running', 'Running', 'distance', 'official'),
    ('cycling', 'Cycling', 'distance', 'official'),
    ('rowing-machine', 'Rowing Machine', 'distance', 'official')
on conflict (slug) do nothing;

insert into exercise_muscles (exercise_id, muscle_group_id, role)
select e.id, m.id, v.role::muscle_role
  from (values
    ('bench-press', 'chest', 'primary'), ('bench-press', 'triceps', 'secondary'), ('bench-press', 'front-delts', 'secondary'),
    ('chest-fly', 'chest', 'primary'), ('chest-fly', 'front-delts', 'secondary'),
    ('push-up', 'chest', 'primary'), ('push-up', 'triceps', 'secondary'), ('push-up', 'front-delts', 'secondary'),
    ('dip', 'chest', 'primary'), ('dip', 'triceps', 'primary'), ('dip', 'front-delts', 'secondary'),
    ('lat-pulldown', 'lats', 'primary'), ('lat-pulldown', 'biceps', 'secondary'), ('lat-pulldown', 'upper-back', 'secondary'),
    ('pull-up', 'lats', 'primary'), ('pull-up', 'biceps', 'secondary'), ('pull-up', 'upper-back', 'secondary'),
    ('row', 'upper-back', 'primary'), ('row', 'lats', 'primary'), ('row', 'biceps', 'secondary'), ('row', 'rear-delts', 'secondary'),
    ('deadlift', 'hamstrings', 'primary'), ('deadlift', 'glutes', 'primary'), ('deadlift', 'lower-back', 'primary'),
    ('deadlift', 'traps', 'secondary'), ('deadlift', 'forearms', 'secondary'),
    ('overhead-press', 'front-delts', 'primary'), ('overhead-press', 'side-delts', 'secondary'), ('overhead-press', 'triceps', 'secondary'),
    ('lateral-raise', 'side-delts', 'primary'),
    ('rear-delt-fly', 'rear-delts', 'primary'), ('rear-delt-fly', 'upper-back', 'secondary'),
    ('face-pull', 'rear-delts', 'primary'), ('face-pull', 'upper-back', 'secondary'),
    ('shrug', 'traps', 'primary'),
    ('bicep-curl', 'biceps', 'primary'), ('bicep-curl', 'forearms', 'secondary'),
    ('hammer-curl', 'biceps', 'primary'), ('hammer-curl', 'forearms', 'primary'),
    ('tricep-extension', 'triceps', 'primary'),
    ('tricep-pushdown', 'triceps', 'primary'),
    ('wrist-curl', 'forearms', 'primary'),
    ('squat', 'quads', 'primary'), ('squat', 'glutes', 'primary'), ('squat', 'hamstrings', 'secondary'), ('squat', 'lower-back', 'secondary'),
    ('leg-press', 'quads', 'primary'), ('leg-press', 'glutes', 'secondary'),
    ('lunge', 'quads', 'primary'), ('lunge', 'glutes', 'primary'), ('lunge', 'hamstrings', 'secondary'),
    ('romanian-deadlift', 'hamstrings', 'primary'), ('romanian-deadlift', 'glutes', 'primary'), ('romanian-deadlift', 'lower-back', 'secondary'),
    ('leg-extension', 'quads', 'primary'),
    ('leg-curl', 'hamstrings', 'primary'),
    ('hip-thrust', 'glutes', 'primary'), ('hip-thrust', 'hamstrings', 'secondary'),
    ('calf-raise', 'calves', 'primary'),
    ('plank', 'abs', 'primary'), ('plank', 'lower-back', 'secondary'),
    ('crunch', 'abs', 'primary'),
    ('leg-raise', 'abs', 'primary')
  ) as v (exercise_slug, muscle_slug, role)
  join exercises e on e.slug = v.exercise_slug
  join muscle_groups m on m.slug = v.muscle_slug
on conflict do nothing;

-- Aliases are globally unique once normalised (lowercase, punctuation to
-- spaces), so each spelling below must differ from every other after that.
insert into exercise_aliases (exercise_id, alias)
select e.id, v.alias
  from (values
    ('bench-press', 'Chest Press'), ('bench-press', 'Bench'),
    ('chest-fly', 'Pec Fly'), ('chest-fly', 'Pec Deck'), ('chest-fly', 'Chest Flys'), ('chest-fly', 'Chest Flyes'),
    ('push-up', 'Pushup'), ('push-up', 'Push Ups'), ('push-up', 'Pushups'),
    ('dip', 'Dips'), ('dip', 'Parallel Bar Dip'),
    ('lat-pulldown', 'Lat Pulldowns'), ('lat-pulldown', 'Lat Pull Down'), ('lat-pulldown', 'Pulldown'), ('lat-pulldown', 'Pulldowns'),
    ('pull-up', 'Pull Ups'), ('pull-up', 'Pullup'), ('pull-up', 'Pullups'),
    ('row', 'Rows'),
    ('deadlift', 'Deadlifts'), ('deadlift', 'Conventional Deadlift'),
    ('overhead-press', 'Shoulder Press'), ('overhead-press', 'Military Press'), ('overhead-press', 'OHP'),
    ('lateral-raise', 'Lateral Raises'), ('lateral-raise', 'Side Raise'), ('lateral-raise', 'Side Lateral Raise'),
    ('rear-delt-fly', 'Reverse Fly'), ('rear-delt-fly', 'Reverse Flys'), ('rear-delt-fly', 'Rear Delt Raise'),
    ('face-pull', 'Face Pulls'),
    ('shrug', 'Shrugs'), ('shrug', 'Trap Shrug'),
    ('bicep-curl', 'Biceps Curl'), ('bicep-curl', 'Bicep Curls'), ('bicep-curl', 'Biceps Curls'), ('bicep-curl', 'Curl'), ('bicep-curl', 'Curls'),
    ('hammer-curl', 'Hammer Curls'),
    ('tricep-extension', 'Triceps Extension'), ('tricep-extension', 'Tricep Extensions'), ('tricep-extension', 'Triceps Extensions'),
    ('tricep-pushdown', 'Triceps Pushdown'), ('tricep-pushdown', 'Tricep Pushdowns'), ('tricep-pushdown', 'Pushdown'), ('tricep-pushdown', 'Pushdowns'),
    ('wrist-curl', 'Wrist Curls'),
    ('squat', 'Squats'),
    ('leg-press', 'Leg Presses'),
    ('lunge', 'Lunges'),
    ('romanian-deadlift', 'RDL'), ('romanian-deadlift', 'Romanian Deadlifts'),
    ('leg-extension', 'Leg Extensions'),
    ('leg-curl', 'Leg Curls'), ('leg-curl', 'Hamstring Curl'), ('leg-curl', 'Hamstring Curls'),
    ('hip-thrust', 'Hip Thrusts'),
    ('calf-raise', 'Calf Raises'),
    ('plank', 'Planks'),
    ('crunch', 'Crunches'),
    ('leg-raise', 'Leg Raises'), ('leg-raise', 'Hanging Leg Raise'), ('leg-raise', 'Hanging Leg Raises'),
    ('running', 'Run'), ('running', 'Jog'), ('running', 'Jogging'),
    ('cycling', 'Bike'), ('cycling', 'Biking'), ('cycling', 'Cycle'),
    ('rowing-machine', 'Rower'), ('rowing-machine', 'Rowing')
  ) as v (exercise_slug, alias)
  join exercises e on e.slug = v.exercise_slug
on conflict do nothing;

-- Tags suggested in the UI for a few exercises. Not a restriction.
insert into exercise_variation_tags (exercise_id, tag_id)
select e.id, t.id
  from (values
    ('bench-press', 'barbell'), ('bench-press', 'dumbbell'), ('bench-press', 'smith-machine'), ('bench-press', 'machine'),
    ('bench-press', 'incline'), ('bench-press', 'decline'), ('bench-press', 'flat'), ('bench-press', 'close-grip'), ('bench-press', 'paused'),
    ('chest-fly', 'dumbbell'), ('chest-fly', 'cable'), ('chest-fly', 'machine'), ('chest-fly', 'incline'),
    ('lat-pulldown', 'cable'), ('lat-pulldown', 'machine'), ('lat-pulldown', 'wide-grip'), ('lat-pulldown', 'close-grip'),
    ('lat-pulldown', 'neutral-grip'), ('lat-pulldown', 'underhand-grip'), ('lat-pulldown', 'single-arm'),
    ('pull-up', 'wide-grip'), ('pull-up', 'close-grip'), ('pull-up', 'neutral-grip'), ('pull-up', 'underhand-grip'),
    ('row', 'barbell'), ('row', 'dumbbell'), ('row', 'cable'), ('row', 'machine'), ('row', 'seated'),
    ('row', 'single-arm'), ('row', 'wide-grip'), ('row', 'neutral-grip'), ('row', 'underhand-grip'), ('row', 'v-bar'),
    ('overhead-press', 'barbell'), ('overhead-press', 'dumbbell'), ('overhead-press', 'machine'), ('overhead-press', 'seated'), ('overhead-press', 'standing'),
    ('lateral-raise', 'dumbbell'), ('lateral-raise', 'cable'), ('lateral-raise', 'machine'), ('lateral-raise', 'single-arm'),
    ('bicep-curl', 'barbell'), ('bicep-curl', 'dumbbell'), ('bicep-curl', 'cable'), ('bicep-curl', 'ez-bar'),
    ('bicep-curl', 'incline'), ('bicep-curl', 'seated'), ('bicep-curl', 'standing'),
    ('tricep-extension', 'cable'), ('tricep-extension', 'dumbbell'), ('tricep-extension', 'barbell'), ('tricep-extension', 'ez-bar'),
    ('tricep-extension', 'rope'), ('tricep-extension', 'v-bar'), ('tricep-extension', 'straight-bar'), ('tricep-extension', 'overhead'),
    ('tricep-extension', 'single-arm'), ('tricep-extension', 'lying'),
    ('tricep-pushdown', 'cable'), ('tricep-pushdown', 'rope'), ('tricep-pushdown', 'v-bar'), ('tricep-pushdown', 'straight-bar'),
    ('tricep-pushdown', 'single-arm'),
    ('squat', 'barbell'), ('squat', 'dumbbell'), ('squat', 'smith-machine'), ('squat', 'machine'), ('squat', 'single-leg'), ('squat', 'paused'),
    ('leg-press', 'machine'), ('leg-press', 'single-leg'),
    ('lunge', 'barbell'), ('lunge', 'dumbbell'), ('lunge', 'single-leg'),
    ('deadlift', 'barbell'), ('deadlift', 'dumbbell'), ('deadlift', 'single-leg'),
    ('romanian-deadlift', 'barbell'), ('romanian-deadlift', 'dumbbell'), ('romanian-deadlift', 'single-leg'),
    ('leg-curl', 'machine'), ('leg-curl', 'seated'), ('leg-curl', 'lying'),
    ('calf-raise', 'machine'), ('calf-raise', 'seated'), ('calf-raise', 'standing'), ('calf-raise', 'single-leg')
  ) as v (exercise_slug, tag_slug)
  join exercises e on e.slug = v.exercise_slug
  join variation_tags t on t.slug = v.tag_slug
on conflict do nothing;
