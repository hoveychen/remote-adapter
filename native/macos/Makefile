# Build the macOS DYLD interpose dylib.
#
# The dylib is loaded into a re-signed copy of claude via DYLD_INSERT_LIBRARIES
# (the adapter's --resign flag prepares that copy). See ../README.md.

DYLIB := rcc_interpose.dylib
SRC   := rcc_interpose.c
CFLAGS ?= -O2 -Wall -Wextra -fPIC

.PHONY: all clean
all: $(DYLIB)

$(DYLIB): $(SRC)
	$(CC) $(CFLAGS) -dynamiclib -o $@ $<

clean:
	rm -f $(DYLIB)
