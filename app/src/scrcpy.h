#ifndef SCRCPY_H
#define SCRCPY_H

#include "common.h"

#include "options.h"

enum scrcpy_exit_code {
    // Normal program termination
    SCRCPY_EXIT_SUCCESS,

    // No connection could be established
    SCRCPY_EXIT_FAILURE,

    // Device was disconnected while running
    SCRCPY_EXIT_DISCONNECTED,
    // A session supervisor requested a transport switch (never a user close).
    SCRCPY_EXIT_ROUTE_SWITCH,
};

enum scrcpy_exit_code
scrcpy(struct scrcpy_options *options);

#endif
