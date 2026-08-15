#ifndef ELOQI_NATIVE_DARWIN_H
#define ELOQI_NATIVE_DARWIN_H

#include <stddef.h>
#include <stdint.h>

typedef struct eloqi_event_tap eloqi_event_tap;

typedef struct {
    uint16_t keycode;
    int pressed;
} eloqi_key_event;

eloqi_event_tap *eloqi_event_tap_create(void);
int eloqi_event_tap_next(eloqi_event_tap *tap, double timeout_seconds,
                         eloqi_key_event *event);
void eloqi_event_tap_destroy(eloqi_event_tap *tap);

typedef struct eloqi_audio_capture eloqi_audio_capture;

int32_t eloqi_audio_create(eloqi_audio_capture **capture);
int32_t eloqi_audio_start(eloqi_audio_capture *capture);
int32_t eloqi_audio_read(eloqi_audio_capture *capture, uint8_t *destination,
                         size_t capacity, size_t *count);
int32_t eloqi_audio_stop(eloqi_audio_capture *capture, uint8_t **tail,
                         size_t *tail_length);
int32_t eloqi_audio_close(eloqi_audio_capture *capture);

int eloqi_clipboard_read(uint8_t **bytes, size_t *length);
int eloqi_clipboard_write(const uint8_t *bytes, size_t length);
int eloqi_post_paste(void);

void eloqi_overlay_run_helper(void);

#endif
