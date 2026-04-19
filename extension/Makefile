EXTENSION  = pgao
EXTVERSION = 0.1.0

DATA = pgao--$(EXTVERSION).sql

REGRESS       = basic health table_bloat
REGRESS_OPTS  = --temp-instance=/tmp/pgao-regress --no-locale --encoding=UTF8

PG_CONFIG ?= pg_config
PGXS      := $(shell $(PG_CONFIG) --pgxs)
include $(PGXS)
