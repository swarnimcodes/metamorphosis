-- Gyms get an area and a maps link, and the directory is seeded with verified gyms.

alter table gyms add column area text;
alter table gyms add column maps_url text check (maps_url ~ '^https://' and length(maps_url) <= 500);
comment on column gyms.area is 'Neighbourhood, e.g. "Kalyani Nagar". address is the street address.';
comment on column gyms.maps_url is 'Link to the place on a maps service. Stored and opened, never fetched or resolved by the server.';

-- Cult, Kalyani Nagar, Pune. Where an earlier hand-made row exists (local databases), update it in place
-- so its workouts and saved-gym links carry over; otherwise insert it.
with legacy as (
    update gyms
       set name = 'Cult Kalyani Nagar', chain = 'Cult', area = 'Kalyani Nagar', city = 'Pune', country_code = 'IN',
           lat = 18.5492421, lng = 73.9060673, maps_url = 'https://maps.app.goo.gl/9nog9QtzoJD9qtGQA', verified = true
     where name = 'Cult, Kalyani Nagar' and deleted_at is null
    returning id
)
insert into gyms (name, chain, area, city, country_code, lat, lng, maps_url, verified)
select 'Cult Kalyani Nagar', 'Cult', 'Kalyani Nagar', 'Pune', 'IN', 18.5492421, 73.9060673,
       'https://maps.app.goo.gl/9nog9QtzoJD9qtGQA', true
 where not exists (select 1 from legacy)
   and not exists (select 1 from gyms where name = 'Cult Kalyani Nagar' and deleted_at is null);
