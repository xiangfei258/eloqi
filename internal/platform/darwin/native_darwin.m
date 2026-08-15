//go:build darwin && cgo

#import "native_darwin.h"

#import <ApplicationServices/ApplicationServices.h>
#import <AudioToolbox/AudioToolbox.h>
#import <Cocoa/Cocoa.h>
#import <QuartzCore/QuartzCore.h>

#include <pthread.h>
#include <stdbool.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

// -------------------------------------------------------------------------
// CGEventTap hotkey observer

#define ELOQI_EVENT_QUEUE_CAPACITY 512

struct eloqi_event_tap {
    CFMachPortRef port;
    CFRunLoopSourceRef source;
    eloqi_key_event events[ELOQI_EVENT_QUEUE_CAPACITY];
    unsigned int head;
    unsigned int tail;
};

static CGEventRef eloqi_event_callback(CGEventTapProxy proxy, CGEventType type,
                                       CGEventRef event, void *context) {
    (void)proxy;
    struct eloqi_event_tap *tap = context;
    if (type == kCGEventTapDisabledByTimeout ||
        type == kCGEventTapDisabledByUserInput) {
        CGEventTapEnable(tap->port, true);
        return event;
    }
    if (type != kCGEventKeyDown && type != kCGEventKeyUp &&
        type != kCGEventFlagsChanged) {
        return event;
    }

    int64_t source_pid = CGEventGetIntegerValueField(
        event, kCGEventSourceUnixProcessID);
    if (source_pid == (int64_t)getpid()) {
        return event;
    }

    uint16_t keycode = (uint16_t)CGEventGetIntegerValueField(
        event, kCGKeyboardEventKeycode);
    int pressed;
    if (type == kCGEventKeyDown) {
        pressed = 1;
    } else if (type == kCGEventKeyUp) {
        pressed = 0;
    } else {
        pressed = CGEventSourceKeyState(
            kCGEventSourceStateCombinedSessionState, (CGKeyCode)keycode) ? 1 : 0;
    }

    unsigned int next = (tap->head + 1) % ELOQI_EVENT_QUEUE_CAPACITY;
    if (next == tap->tail) {
        // Never stall the WindowServer callback. Drop the oldest observation;
        // the next physical state change reconciles the edge machine.
        tap->tail = (tap->tail + 1) % ELOQI_EVENT_QUEUE_CAPACITY;
    }
    tap->events[tap->head].keycode = keycode;
    tap->events[tap->head].pressed = pressed;
    tap->head = next;
    return event;
}

eloqi_event_tap *eloqi_event_tap_create(void) {
    struct eloqi_event_tap *tap = calloc(1, sizeof(*tap));
    if (tap == NULL) {
        return NULL;
    }
    CGEventMask mask = CGEventMaskBit(kCGEventKeyDown) |
                       CGEventMaskBit(kCGEventKeyUp) |
                       CGEventMaskBit(kCGEventFlagsChanged);
    tap->port = CGEventTapCreate(kCGSessionEventTap, kCGHeadInsertEventTap,
                                 kCGEventTapOptionListenOnly, mask,
                                 eloqi_event_callback, tap);
    if (tap->port == NULL) {
        free(tap);
        return NULL;
    }
    tap->source = CFMachPortCreateRunLoopSource(kCFAllocatorDefault,
                                                tap->port, 0);
    if (tap->source == NULL) {
        CFRelease(tap->port);
        free(tap);
        return NULL;
    }
    CFRunLoopAddSource(CFRunLoopGetCurrent(), tap->source,
                       kCFRunLoopDefaultMode);
    CGEventTapEnable(tap->port, true);
    return tap;
}

int eloqi_event_tap_next(eloqi_event_tap *tap, double timeout_seconds,
                         eloqi_key_event *event) {
    if (tap == NULL || event == NULL) {
        return -1;
    }
    if (tap->tail == tap->head) {
        CFRunLoopRunInMode(kCFRunLoopDefaultMode, timeout_seconds, true);
    }
    if (tap->tail == tap->head) {
        return 0;
    }
    *event = tap->events[tap->tail];
    tap->tail = (tap->tail + 1) % ELOQI_EVENT_QUEUE_CAPACITY;
    return 1;
}

void eloqi_event_tap_destroy(eloqi_event_tap *tap) {
    if (tap == NULL) {
        return;
    }
    CGEventTapEnable(tap->port, false);
    CFRunLoopRemoveSource(CFRunLoopGetCurrent(), tap->source,
                          kCFRunLoopDefaultMode);
    CFRelease(tap->source);
    CFRelease(tap->port);
    free(tap);
}

// -------------------------------------------------------------------------
// AudioQueue recorder

#define ELOQI_AUDIO_BUFFER_COUNT 4
#define ELOQI_AUDIO_BUFFER_BYTES 4096
#define ELOQI_AUDIO_QUEUE_LIMIT (1024 * 1024)
#define ELOQI_AUDIO_OVERFLOW_STATUS ((int32_t)-66701)
#define ELOQI_PARAM_STATUS ((int32_t)-50)
#define ELOQI_MEMORY_STATUS ((int32_t)-108)

struct eloqi_audio_chunk {
    struct eloqi_audio_chunk *next;
    size_t length;
    size_t offset;
    uint8_t bytes[];
};

struct eloqi_audio_capture {
    AudioQueueRef queue;
    AudioQueueBufferRef buffers[ELOQI_AUDIO_BUFFER_COUNT];
    pthread_mutex_t mutex;
    pthread_cond_t condition;
    int started;
    int stopping;
    int closed;
    int32_t callback_error;
    size_t buffered;
    struct eloqi_audio_chunk *head;
    struct eloqi_audio_chunk *tail;
};

static void eloqi_audio_free_chunks(struct eloqi_audio_capture *capture) {
    struct eloqi_audio_chunk *chunk = capture->head;
    while (chunk != NULL) {
        struct eloqi_audio_chunk *next = chunk->next;
        free(chunk);
        chunk = next;
    }
    capture->head = NULL;
    capture->tail = NULL;
    capture->buffered = 0;
}

static void eloqi_audio_callback(void *context, AudioQueueRef queue,
                                 AudioQueueBufferRef buffer,
                                 const AudioTimeStamp *start_time,
                                 UInt32 packet_count,
                                 const AudioStreamPacketDescription *packets) {
    (void)start_time;
    (void)packet_count;
    (void)packets;
    struct eloqi_audio_capture *capture = context;

    pthread_mutex_lock(&capture->mutex);
    if (!capture->closed && buffer->mAudioDataByteSize > 0) {
        size_t length = (size_t)buffer->mAudioDataByteSize;
        if (capture->buffered + length > ELOQI_AUDIO_QUEUE_LIMIT) {
            capture->callback_error = ELOQI_AUDIO_OVERFLOW_STATUS;
        } else {
            struct eloqi_audio_chunk *chunk =
                malloc(sizeof(*chunk) + length);
            if (chunk == NULL) {
                capture->callback_error = ELOQI_MEMORY_STATUS;
            } else {
                chunk->next = NULL;
                chunk->length = length;
                chunk->offset = 0;
                memcpy(chunk->bytes, buffer->mAudioData, length);
                if (capture->tail == NULL) {
                    capture->head = chunk;
                } else {
                    capture->tail->next = chunk;
                }
                capture->tail = chunk;
                capture->buffered += length;
            }
        }
    }
    int requeue = capture->started && !capture->stopping && !capture->closed;
    pthread_cond_broadcast(&capture->condition);
    pthread_mutex_unlock(&capture->mutex);

    if (requeue) {
        buffer->mAudioDataByteSize = 0;
        OSStatus status = AudioQueueEnqueueBuffer(queue, buffer, 0, NULL);
        if (status != noErr) {
            pthread_mutex_lock(&capture->mutex);
            capture->callback_error = (int32_t)status;
            pthread_cond_broadcast(&capture->condition);
            pthread_mutex_unlock(&capture->mutex);
        }
    }
}

int32_t eloqi_audio_create(eloqi_audio_capture **output) {
    if (output == NULL) {
        return ELOQI_PARAM_STATUS;
    }
    *output = NULL;
    struct eloqi_audio_capture *capture = calloc(1, sizeof(*capture));
    if (capture == NULL) {
        return ELOQI_MEMORY_STATUS;
    }
    pthread_mutex_init(&capture->mutex, NULL);
    pthread_cond_init(&capture->condition, NULL);

    AudioStreamBasicDescription format;
    memset(&format, 0, sizeof(format));
    format.mSampleRate = 16000.0;
    format.mFormatID = kAudioFormatLinearPCM;
    format.mFormatFlags = kLinearPCMFormatFlagIsSignedInteger |
                          kLinearPCMFormatFlagIsPacked;
    format.mBytesPerPacket = 2;
    format.mFramesPerPacket = 1;
    format.mBytesPerFrame = 2;
    format.mChannelsPerFrame = 1;
    format.mBitsPerChannel = 16;

    OSStatus status = AudioQueueNewInput(&format, eloqi_audio_callback,
                                          capture, NULL, NULL, 0,
                                          &capture->queue);
    if (status != noErr) {
        pthread_cond_destroy(&capture->condition);
        pthread_mutex_destroy(&capture->mutex);
        free(capture);
        return (int32_t)status;
    }
    for (int index = 0; index < ELOQI_AUDIO_BUFFER_COUNT; ++index) {
        status = AudioQueueAllocateBuffer(capture->queue,
                                           ELOQI_AUDIO_BUFFER_BYTES,
                                           &capture->buffers[index]);
        if (status != noErr) {
            AudioQueueDispose(capture->queue, true);
            pthread_cond_destroy(&capture->condition);
            pthread_mutex_destroy(&capture->mutex);
            free(capture);
            return (int32_t)status;
        }
        status = AudioQueueEnqueueBuffer(capture->queue,
                                          capture->buffers[index], 0, NULL);
        if (status != noErr) {
            AudioQueueDispose(capture->queue, true);
            pthread_cond_destroy(&capture->condition);
            pthread_mutex_destroy(&capture->mutex);
            free(capture);
            return (int32_t)status;
        }
    }
    *output = capture;
    return 0;
}

int32_t eloqi_audio_start(eloqi_audio_capture *capture) {
    if (capture == NULL) {
        return ELOQI_PARAM_STATUS;
    }
    pthread_mutex_lock(&capture->mutex);
    if (capture->closed || capture->started) {
        pthread_mutex_unlock(&capture->mutex);
        return ELOQI_PARAM_STATUS;
    }
    capture->stopping = 0;
    capture->callback_error = 0;
    capture->started = 1;
    pthread_mutex_unlock(&capture->mutex);

    OSStatus status = AudioQueueStart(capture->queue, NULL);
    if (status != noErr) {
        pthread_mutex_lock(&capture->mutex);
        capture->started = 0;
        pthread_mutex_unlock(&capture->mutex);
    }
    return (int32_t)status;
}

int32_t eloqi_audio_read(eloqi_audio_capture *capture, uint8_t *destination,
                         size_t capacity, size_t *count) {
    if (capture == NULL || count == NULL ||
        (destination == NULL && capacity != 0)) {
        return ELOQI_PARAM_STATUS;
    }
    *count = 0;
    pthread_mutex_lock(&capture->mutex);
    while (capture->head == NULL && capture->started &&
           !capture->stopping && !capture->closed &&
           capture->callback_error == 0) {
        pthread_cond_wait(&capture->condition, &capture->mutex);
    }
    if (capture->stopping || capture->closed || !capture->started) {
        pthread_mutex_unlock(&capture->mutex);
        return 1; // EOF
    }
    if (capture->head == NULL && capture->callback_error != 0) {
        int32_t error = capture->callback_error;
        pthread_mutex_unlock(&capture->mutex);
        return error;
    }
    if (capacity == 0 || capture->head == NULL) {
        pthread_mutex_unlock(&capture->mutex);
        return 0;
    }

    struct eloqi_audio_chunk *chunk = capture->head;
    size_t available = chunk->length - chunk->offset;
    size_t copied = capacity < available ? capacity : available;
    memcpy(destination, chunk->bytes + chunk->offset, copied);
    chunk->offset += copied;
    capture->buffered -= copied;
    if (chunk->offset == chunk->length) {
        capture->head = chunk->next;
        if (capture->head == NULL) {
            capture->tail = NULL;
        }
        free(chunk);
    }
    *count = copied;
    pthread_mutex_unlock(&capture->mutex);
    return 0;
}

int32_t eloqi_audio_stop(eloqi_audio_capture *capture, uint8_t **tail,
                         size_t *tail_length) {
    if (capture == NULL || tail == NULL || tail_length == NULL) {
        return ELOQI_PARAM_STATUS;
    }
    *tail = NULL;
    *tail_length = 0;

    pthread_mutex_lock(&capture->mutex);
    int was_started = capture->started;
    capture->stopping = 1;
    pthread_cond_broadcast(&capture->condition);
    pthread_mutex_unlock(&capture->mutex);

    OSStatus stop_status = noErr;
    if (was_started) {
        // The voice layer already records its configurable tail delay before
        // Stop. An immediate AudioQueue stop gives this platform method a
        // bounded shutdown path if the input device disappears mid-session.
        stop_status = AudioQueueStop(capture->queue, true);
    }

    pthread_mutex_lock(&capture->mutex);
    capture->started = 0;
    if (capture->buffered != 0) {
        uint8_t *bytes = malloc(capture->buffered);
        if (bytes == NULL) {
            pthread_mutex_unlock(&capture->mutex);
            return ELOQI_MEMORY_STATUS;
        }
        size_t offset = 0;
        for (struct eloqi_audio_chunk *chunk = capture->head;
             chunk != NULL; chunk = chunk->next) {
            size_t length = chunk->length - chunk->offset;
            memcpy(bytes + offset, chunk->bytes + chunk->offset, length);
            offset += length;
        }
        *tail = bytes;
        *tail_length = offset;
    }
    int32_t callback_error = capture->callback_error;
    eloqi_audio_free_chunks(capture);
    pthread_cond_broadcast(&capture->condition);
    pthread_mutex_unlock(&capture->mutex);

    if (stop_status != noErr) {
        return (int32_t)stop_status;
    }
    return callback_error;
}

int32_t eloqi_audio_close(eloqi_audio_capture *capture) {
    if (capture == NULL) {
        return 0;
    }
    pthread_mutex_lock(&capture->mutex);
    int was_started = capture->started;
    capture->closed = 1;
    capture->stopping = 1;
    pthread_cond_broadcast(&capture->condition);
    pthread_mutex_unlock(&capture->mutex);

    if (was_started) {
        AudioQueueStop(capture->queue, true);
    }
    OSStatus status = AudioQueueDispose(capture->queue, true);
    pthread_mutex_lock(&capture->mutex);
    eloqi_audio_free_chunks(capture);
    pthread_mutex_unlock(&capture->mutex);
    pthread_cond_destroy(&capture->condition);
    pthread_mutex_destroy(&capture->mutex);
    free(capture);
    return (int32_t)status;
}

// -------------------------------------------------------------------------
// NSPasteboard and native Command+V injection

int eloqi_clipboard_read(uint8_t **bytes, size_t *length) {
    if (bytes == NULL || length == NULL) {
        return 0;
    }
    *bytes = NULL;
    *length = 0;
    @autoreleasepool {
        NSString *text = [[NSPasteboard generalPasteboard]
            stringForType:NSPasteboardTypeString];
        if (text == nil) {
            return 1;
        }
        NSData *data = [text dataUsingEncoding:NSUTF8StringEncoding];
        size_t size = (size_t)[data length];
        uint8_t *copy = malloc(size == 0 ? 1 : size);
        if (copy == NULL) {
            return 0;
        }
        if (size != 0) {
            memcpy(copy, [data bytes], size);
        }
        *bytes = copy;
        *length = size;
        return 1;
    }
}

int eloqi_clipboard_write(const uint8_t *bytes, size_t length) {
    if (bytes == NULL && length != 0) {
        return 0;
    }
    @autoreleasepool {
        NSString *text = [[NSString alloc] initWithBytes:bytes
                                                   length:length
                                                 encoding:NSUTF8StringEncoding];
        if (text == nil) {
            return 0;
        }
        NSPasteboard *pasteboard = [NSPasteboard generalPasteboard];
        [pasteboard clearContents];
        BOOL written = [pasteboard setString:text
                                      forType:NSPasteboardTypeString];
        [text release];
        return written ? 1 : 0;
    }
}

int eloqi_post_paste(void) {
    if (!CGPreflightPostEventAccess()) {
        return -1;
    }
    CGEventSourceRef source = CGEventSourceCreate(
        kCGEventSourceStateCombinedSessionState);
    if (source == NULL) {
        return -2;
    }
    CGEventRef command_down = CGEventCreateKeyboardEvent(source, 0x37, true);
    CGEventRef v_down = CGEventCreateKeyboardEvent(source, 0x09, true);
    CGEventRef v_up = CGEventCreateKeyboardEvent(source, 0x09, false);
    CGEventRef command_up = CGEventCreateKeyboardEvent(source, 0x37, false);
    if (command_down == NULL || v_down == NULL || v_up == NULL ||
        command_up == NULL) {
        if (command_down != NULL) CFRelease(command_down);
        if (v_down != NULL) CFRelease(v_down);
        if (v_up != NULL) CFRelease(v_up);
        if (command_up != NULL) CFRelease(command_up);
        CFRelease(source);
        return -2;
    }
    CGEventSetFlags(v_down, kCGEventFlagMaskCommand);
    CGEventSetFlags(v_up, kCGEventFlagMaskCommand);
    CGEventPost(kCGHIDEventTap, command_down);
    CGEventPost(kCGHIDEventTap, v_down);
    CGEventPost(kCGHIDEventTap, v_up);
    CGEventPost(kCGHIDEventTap, command_up);
    CFRelease(command_down);
    CFRelease(v_down);
    CFRelease(v_up);
    CFRelease(command_up);
    CFRelease(source);
    return 0;
}

// -------------------------------------------------------------------------
// NSPanel overlay helper process

@interface EloquiOverlayController : NSObject <NSApplicationDelegate> {
    NSPanel *_panel;
    NSTextField *_label;
    NSView *_content;
    NSFileHandle *_input;
    NSMutableString *_pending;
}
@end

@implementation EloquiOverlayController

- (void)applicationDidFinishLaunching:(NSNotification *)notification {
    (void)notification;
    NSRect frame = NSMakeRect(0, 0, 360, 54);
    _panel = [[NSPanel alloc]
        initWithContentRect:frame
                  styleMask:(NSWindowStyleMaskBorderless |
                             NSWindowStyleMaskNonactivatingPanel)
                    backing:NSBackingStoreBuffered
                      defer:NO];
    [_panel setOpaque:NO];
    [_panel setBackgroundColor:[NSColor clearColor]];
    [_panel setHasShadow:YES];
    [_panel setLevel:NSStatusWindowLevel];
    [_panel setHidesOnDeactivate:NO];
    [_panel setIgnoresMouseEvents:YES];
    [_panel setCollectionBehavior:(NSWindowCollectionBehaviorCanJoinAllSpaces |
                                    NSWindowCollectionBehaviorFullScreenAuxiliary)];

    _content = [[NSView alloc] initWithFrame:frame];
    [_content setWantsLayer:YES];
    [[_content layer] setCornerRadius:27.0];
    [[_content layer] setMasksToBounds:YES];
    [_panel setContentView:_content];

    _label = [[NSTextField alloc] initWithFrame:NSInsetRect(frame, 18, 8)];
    [_label setEditable:NO];
    [_label setSelectable:NO];
    [_label setBezeled:NO];
    [_label setDrawsBackground:NO];
    [_label setAlignment:NSTextAlignmentCenter];
    [_label setTextColor:[NSColor whiteColor]];
    [_label setFont:[NSFont systemFontOfSize:14 weight:NSFontWeightSemibold]];
    [[_label cell] setLineBreakMode:NSLineBreakByTruncatingTail];
    [_content addSubview:_label];

    NSScreen *screen = [NSScreen mainScreen];
    NSRect visible = [screen visibleFrame];
    [_panel setFrameOrigin:NSMakePoint(
        NSMidX(visible) - NSWidth(frame) / 2.0,
        NSMinY(visible) + 44.0)];

    _pending = [[NSMutableString alloc] init];
    _input = [[NSFileHandle fileHandleWithStandardInput] retain];
    [[NSNotificationCenter defaultCenter]
        addObserver:self
           selector:@selector(inputReady:)
               name:NSFileHandleReadCompletionNotification
             object:_input];
    [_input readInBackgroundAndNotify];
}

- (void)inputReady:(NSNotification *)notification {
    NSData *data = [[notification userInfo]
        objectForKey:NSFileHandleNotificationDataItem];
    if ([data length] == 0) {
        [NSApp terminate:nil];
        return;
    }
    NSString *chunk = [[NSString alloc] initWithData:data
                                            encoding:NSUTF8StringEncoding];
    if (chunk != nil) {
        [_pending appendString:chunk];
        [chunk release];
    }
    while (true) {
        NSRange newline = [_pending rangeOfString:@"\n"];
        if (newline.location == NSNotFound) {
            break;
        }
        NSString *line = [_pending substringToIndex:newline.location];
        [_pending deleteCharactersInRange:NSMakeRange(0, NSMaxRange(newline))];
        [self applyCommand:line];
    }
    [_input readInBackgroundAndNotify];
}

- (void)applyCommand:(NSString *)line {
    if ([line isEqualToString:@"hide"]) {
        [_panel orderOut:nil];
        return;
    }
    if ([line isEqualToString:@"close"]) {
        [NSApp terminate:nil];
        return;
    }
    NSArray *parts = [line componentsSeparatedByString:@"\t"];
    if ([parts count] != 3 || ![[parts objectAtIndex:0] isEqualToString:@"show"]) {
        return;
    }
    NSString *state = [parts objectAtIndex:1];
    NSData *messageData = [[NSData alloc]
        initWithBase64EncodedString:[parts objectAtIndex:2] options:0];
    NSString *message = [[[NSString alloc] initWithData:messageData
                                               encoding:NSUTF8StringEncoding]
        autorelease];
    [messageData release];

    NSString *title = @"Connecting";
    NSColor *color = [NSColor colorWithCalibratedRed:0.27 green:0.35 blue:0.42 alpha:0.94];
    if ([state isEqualToString:@"recording"]) {
        title = @"Recording";
        color = [NSColor colorWithCalibratedRed:0.84 green:0.21 blue:0.21 alpha:0.94];
    } else if ([state isEqualToString:@"stopping"]) {
        title = @"Finishing";
        color = [NSColor colorWithCalibratedRed:0.68 green:0.43 blue:0.13 alpha:0.94];
    } else if ([state isEqualToString:@"waiting"]) {
        title = @"Recognizing";
        color = [NSColor colorWithCalibratedRed:0.20 green:0.42 blue:0.63 alpha:0.94];
    } else if ([state isEqualToString:@"error"]) {
        title = @"Error";
        color = [NSColor colorWithCalibratedRed:0.72 green:0.16 blue:0.16 alpha:0.94];
    }
    if ([message length] != 0) {
        title = [NSString stringWithFormat:@"%@  -  %@", title, message];
    }
    [_label setStringValue:title];
    [[_content layer] setBackgroundColor:[color CGColor]];
    [_panel orderFrontRegardless];
}

- (void)dealloc {
    [[NSNotificationCenter defaultCenter] removeObserver:self];
    [_input release];
    [_pending release];
    [_label release];
    [_content release];
    [_panel release];
    [super dealloc];
}

@end

void eloqi_overlay_run_helper(void) {
    @autoreleasepool {
        [NSApplication sharedApplication];
        [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
        EloquiOverlayController *controller =
            [[EloquiOverlayController alloc] init];
        [NSApp setDelegate:controller];
        [NSApp run];
        [NSApp setDelegate:nil];
        [controller release];
    }
}
