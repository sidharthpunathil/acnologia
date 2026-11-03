# Plain Makefile for systems without CMake. Needs a C11 compiler and libcurl
# (curl-config or pkg-config finds it).
#
#   make            library, tests and example in build/
#   make test       run the tests
#   make test SAN=1 run the tests under AddressSanitizer and UBSan
#   make clean

CC ?= cc
BUILD ?= $(if $(filter 1,$(SAN)),build-san,build)
CURL_CFLAGS ?= $(shell curl-config --cflags 2>/dev/null || pkg-config --cflags libcurl)
CURL_LIBS ?= $(shell curl-config --libs 2>/dev/null || pkg-config --libs libcurl)

WARN = -Wall -Wextra -Werror -pedantic -Wshadow -Wstrict-prototypes
CFLAGS ?= -O1 -g
ifeq ($(SAN),1)
SANFLAGS = -fsanitize=address,undefined -fno-omit-frame-pointer -fno-sanitize-recover=all
endif
ALL_CFLAGS = -std=c11 $(CFLAGS) $(SANFLAGS) -Iinclude -Isrc -Ithird_party/cjson $(CURL_CFLAGS)
LDLIBS = $(CURL_LIBS)

SRC = src/client.c src/errors.c src/http.c src/util.c
OBJ = $(SRC:src/%.c=$(BUILD)/%.o) $(BUILD)/cJSON.o
LIB = $(BUILD)/libacnologia.a

.PHONY: all test clean

all: $(LIB) $(BUILD)/acnologia_tests $(BUILD)/acnologia_quickstart

$(BUILD):
	mkdir -p $(BUILD)

$(BUILD)/%.o: src/%.c src/internal.h include/acnologia/acnologia.h | $(BUILD)
	$(CC) $(ALL_CFLAGS) $(WARN) -c $< -o $@

$(BUILD)/cJSON.o: third_party/cjson/cJSON.c third_party/cjson/cJSON.h | $(BUILD)
	$(CC) $(ALL_CFLAGS) -Wno-deprecated-declarations -c $< -o $@

$(LIB): $(OBJ)
	$(AR) rcs $@ $(OBJ)

$(BUILD)/acnologia_tests: tests/test_client.c tests/fake_server.c tests/fake_server.h $(LIB)
	$(CC) $(ALL_CFLAGS) $(WARN) tests/test_client.c tests/fake_server.c $(LIB) $(LDLIBS) -pthread -o $@

$(BUILD)/acnologia_quickstart: examples/quickstart.c $(LIB)
	$(CC) $(ALL_CFLAGS) $(WARN) examples/quickstart.c $(LIB) $(LDLIBS) -o $@

test: $(BUILD)/acnologia_tests
	env -u ACNOLOGIA_URL -u ACNOLOGIA_TOKEN -u ACNOLOGIA_ACCESS_PASSWORD -u ACNOLOGIA_ACCESS_URL $(BUILD)/acnologia_tests

clean:
	rm -rf $(BUILD)
