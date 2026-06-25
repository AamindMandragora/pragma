// agentguard.c
#define _GNU_SOURCE
#include <dlfcn.h>
#include <errno.h>
#include <string.h>
#include <stdarg.h>
#include <stdlib.h>
#include <stdio.h>
#include <fcntl.h>
#include <fnmatch.h>

// gets blocklist csv from env
static const char* get_blocklist(void) {
    return getenv("PRAGMA_BLOCKLIST");
}

// loops through each entry in the blocklist and returns whether the path matches one
static int is_blocked(const char *path) {
    const char *list = get_blocklist();
    if (!list || !path) return 0;

    char *copy = strdup(list);
    char *token = strtok(copy, ",");
    while (token) {
        // matches path against pattern token
        if (fnmatch(token, path, FNM_PATHNAME) == 0) {
            free(copy);
            return 1;
        }
        // if path is relative, then try prepending the parent directory to it and match
        if (path[0] != '/') {
            char prefixed[strlen(path) + 3];
            snprintf(prefixed, sizeof(prefixed), "./%s", path);
            if (fnmatch(token, prefixed, FNM_PATHNAME) == 0) {
                free(copy);
                return 1;
            }
        }
        // if token doesn't start with /, check if the actual filename in path matches it
        if (!strchr(token, '/')) {
            const char *base = strrchr(path, '/');
            base = base ? base + 1 : path;
            if (fnmatch(token, base, 0) == 0) {
                free(copy);
                return 1;
            }
        }
        // move to next pattern in csv
        token = strtok(NULL, ",");
    }
    free(copy);
    return 0;
}

// create a function type for open()
typedef int (*real_open_t)(const char*, int, ...);

// override open by setting errno = ENOENT (no entry) if the path is blocked
int open(const char *path, int flags, ...) {
    if (is_blocked(path)) {
        errno = ENOENT;
        return -1;
    }
    // otherwise, get the actual open()
    real_open_t real_open = dlsym(RTLD_NEXT, "open");
    // creates a variable length list of args if necessary and calls open
    if (flags & O_CREAT) {
        va_list args;
        va_start(args, flags);
        int mode = va_arg(args, int);
        va_end(args);
        return real_open(path, flags, mode);
    }
    return real_open(path, flags);
}

// on linux, open64() needs to be overriden as well
#ifndef __APPLE__
int open64(const char *path, int flags, ...) {
    if (is_blocked(path)) {
        errno = ENOENT;
        return -1;
    }
    real_open_t real_open64 = dlsym(RTLD_NEXT, "open64");
    if (flags & O_CREAT) {
        va_list args;
        va_start(args, flags);
        int mode = va_arg(args, int);
        va_end(args);
        return real_open64(path, flags, mode);
    }
    return real_open64(path, flags);
}
#endif

// creates a function type for openat
typedef int (*real_openat_t)(int, const char*, int, ...);

// this is the current standard open syscall, must override
int openat(int dirfd, const char *path, int flags, ...) {
    if (is_blocked(path)) {
        errno = ENOENT;
        return -1;
    }
    real_openat_t real_openat = dlsym(RTLD_NEXT, "openat");
    if (flags & O_CREAT) {
        va_list args;
        va_start(args, flags);
        int mode = va_arg(args, int);
        va_end(args);
        return real_openat(dirfd, path, flags, mode);
    }
    return real_openat(dirfd, path, flags);
}