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
#include <unistd.h>
#include <limits.h>

// the smallest next open fd after stderr
#define DEFAULT_BLOCKLIST_FD 3

// where our blocklist will live
static char *g_blocklist = NULL;

// gets blocklist fd from env
static int blocklist_fd(void) {
    const char *s = getenv("PRAGMA_BLOCKLIST_FD");
    if (!s || !*s) return DEFAULT_BLOCKLIST_FD;
    char *end;
    long fd = strtol(s, &end, 10);
    if (*end != '\0' || fd < 0 || fd > INT_MAX) return DEFAULT_BLOCKLIST_FD;
    return (int)fd;
}

// reads blocklist off of temp file given to us by go
__attribute__((constructor))
static void load_blocklist(void) {
    int fd = blocklist_fd();
    // always rewind before reading
    if (lseek(fd, 0, SEEK_SET) < 0) return;
    size_t cap = 4096, len = 0;
    char *buf = malloc(cap);
    if (!buf) return;
    ssize_t n;
    // leave one byte free for the null terminator
    while (len + 1 < cap && (n = read(fd, buf + len, cap - len - 1)) > 0) {
        len += (size_t)n;
        if (len + 1 == cap) {
            cap *= 2;
            char *tmp = realloc(buf, cap);
            if (!tmp) { free(buf); return; }
            buf = tmp;
        }
    }
    buf[len] = '\0';
    g_blocklist = buf;
}

// gets blocklist csv loaded at startup
static const char* get_blocklist(void) {
    return g_blocklist;
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

// linux LFS variant of openat, same shape as openat
#ifndef __APPLE__
int openat64(int dirfd, const char *path, int flags, ...) {
    if (is_blocked(path)) {
        errno = ENOENT;
        return -1;
    }
    real_openat_t real_openat64 = dlsym(RTLD_NEXT, "openat64");
    if (flags & O_CREAT) {
        va_list args;
        va_start(args, flags);
        int mode = va_arg(args, int);
        va_end(args);
        return real_openat64(dirfd, path, flags, mode);
    }
    return real_openat64(dirfd, path, flags);
}
#endif

// create a function type for creat()
typedef int (*real_creat_t)(const char*, mode_t);

// creat() is equivalent to open(path, O_CREAT|O_WRONLY|O_TRUNC, mode) but is its own libc entry point, so callers that use it directly bypass open()
int creat(const char *path, mode_t mode) {
    if (is_blocked(path)) {
        errno = ENOENT;
        return -1;
    }
    real_creat_t real_creat = dlsym(RTLD_NEXT, "creat");
    return real_creat(path, mode);
}

#ifndef __APPLE__
int creat64(const char *path, mode_t mode) {
    if (is_blocked(path)) {
        errno = ENOENT;
        return -1;
    }
    real_creat_t real_creat64 = dlsym(RTLD_NEXT, "creat64");
    return real_creat64(path, mode);
}
#endif

// create a function type for fopen()
typedef FILE* (*real_fopen_t)(const char*, const char*);

// separate from raw open, need soverride
FILE *fopen(const char *path, const char *mode) {
    if (is_blocked(path)) {
        errno = ENOENT;
        return NULL;
    }
    real_fopen_t real_fopen = dlsym(RTLD_NEXT, "fopen");
    return real_fopen(path, mode);
}

#ifndef __APPLE__
FILE *fopen64(const char *path, const char *mode) {
    if (is_blocked(path)) {
        errno = ENOENT;
        return NULL;
    }
    real_fopen_t real_fopen64 = dlsym(RTLD_NEXT, "fopen64");
    return real_fopen64(path, mode);
}
#endif

// create a function type for freopen()
typedef FILE* (*real_freopen_t)(const char*, const char*, FILE*);

// freopen() redirects an existing FILE* at a new path
FILE *freopen(const char *path, const char *mode, FILE *stream) {
    if (path && is_blocked(path)) {
        // on failure the original stream is still closed
        fclose(stream);
        errno = ENOENT;
        return NULL;
    }
    real_freopen_t real_freopen = dlsym(RTLD_NEXT, "freopen");
    return real_freopen(path, mode, stream);
}

#ifndef __APPLE__
FILE *freopen64(const char *path, const char *mode, FILE *stream) {
    if (path && is_blocked(path)) {
        fclose(stream);
        errno = ENOENT;
        return NULL;
    }
    real_freopen_t real_freopen64 = dlsym(RTLD_NEXT, "freopen64");
    return real_freopen64(path, mode, stream);
}
#endif
