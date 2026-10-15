# metamorphosis

overload progressively


## database stuff

### pre req
- goose cli
- set up required env vars in .env (GOOSE_DRIVER, GOOSE_DBSTRING, GOOSE_MIGRATION_DIR)

### create a new migration

```sh
goose create create_user_table sql
```

this creates a migration file in the specified `GOOSE_MIGRATION_DIR`


### check status

```sh
goose status
```

checks the status of the database as specified by `GOOSE_DBSTRING`


### run migration files

```sh
goose up
```
