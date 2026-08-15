//go:build linux

package linux

/*
#cgo pkg-config: x11
#include <X11/Xlib.h>
#include <X11/Xatom.h>
#include <X11/Xutil.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
    Display *display;
    Window window;
    Pixmap background;
    XFontStruct *font;
    int screen;
    unsigned int width;
    unsigned int height;
} EloqiOverlay;

static void eloqi_overlay_set_atom(Display *display, Window window,
                                   const char *property_name,
                                   const char *value_name) {
    Atom property = XInternAtom(display, property_name, False);
    Atom value = XInternAtom(display, value_name, False);
    XChangeProperty(display, window, property, XA_ATOM, 32,
                    PropModeReplace, (unsigned char *)&value, 1);
}

static EloqiOverlay *eloqi_overlay_create(void) {
    Display *display = XOpenDisplay(NULL);
    if (display == NULL) {
        return NULL;
    }

    EloqiOverlay *overlay = (EloqiOverlay *)calloc(1, sizeof(EloqiOverlay));
    if (overlay == NULL) {
        XCloseDisplay(display);
        return NULL;
    }
    overlay->display = display;
    overlay->screen = DefaultScreen(display);
    overlay->width = 360;
    overlay->height = 52;

    int screen_width = DisplayWidth(display, overlay->screen);
    int screen_height = DisplayHeight(display, overlay->screen);
    if (screen_width < (int)overlay->width + 24) {
        overlay->width = screen_width > 48 ? (unsigned int)(screen_width - 24) : 24;
    }
    int x = (screen_width - (int)overlay->width) / 2;
    int y = screen_height - (int)overlay->height - 48;
    if (y < 0) {
        y = 0;
    }

    XSetWindowAttributes attributes;
    memset(&attributes, 0, sizeof(attributes));
    attributes.override_redirect = True;
    attributes.background_pixel = BlackPixel(display, overlay->screen);
    attributes.event_mask = 0;

    Window root = RootWindow(display, overlay->screen);
    overlay->window = XCreateWindow(
        display, root, x, y, overlay->width, overlay->height, 0,
        CopyFromParent, InputOutput, CopyFromParent,
        CWOverrideRedirect | CWBackPixel | CWEventMask, &attributes);
    if (overlay->window == 0) {
        XCloseDisplay(display);
        free(overlay);
        return NULL;
    }

    XStoreName(display, overlay->window, "Eloqui status");
    XClassHint class_hint;
    class_hint.res_name = "eloqui";
    class_hint.res_class = "Eloqui";
    XSetClassHint(display, overlay->window, &class_hint);

    XWMHints *wm_hints = XAllocWMHints();
    if (wm_hints != NULL) {
        wm_hints->flags = InputHint;
        wm_hints->input = False;
        XSetWMHints(display, overlay->window, wm_hints);
        XFree(wm_hints);
    }

    eloqi_overlay_set_atom(display, overlay->window,
                           "_NET_WM_WINDOW_TYPE",
                           "_NET_WM_WINDOW_TYPE_NOTIFICATION");
    eloqi_overlay_set_atom(display, overlay->window,
                           "_NET_WM_STATE", "_NET_WM_STATE_ABOVE");
    overlay->font = XLoadQueryFont(display, "fixed");
    XFlush(display);
    return overlay;
}

static unsigned long eloqi_overlay_color(EloqiOverlay *overlay,
                                         const char *name,
                                         unsigned long fallback) {
    XColor screen_color;
    XColor exact_color;
    Colormap colormap = DefaultColormap(overlay->display, overlay->screen);
    if (XAllocNamedColor(overlay->display, colormap, name,
                         &screen_color, &exact_color) != 0) {
        return screen_color.pixel;
    }
    return fallback;
}

static int eloqi_overlay_show(EloqiOverlay *overlay, const char *background,
                              const char *text) {
    if (overlay == NULL || overlay->display == NULL || text == NULL) {
        return 0;
    }
    Display *display = overlay->display;
    unsigned long bg = eloqi_overlay_color(
        overlay, background, BlackPixel(display, overlay->screen));
    unsigned long fg = eloqi_overlay_color(
        overlay, "#ffffff", WhitePixel(display, overlay->screen));

    Pixmap pixmap = XCreatePixmap(display, overlay->window,
                                  overlay->width, overlay->height,
                                  DefaultDepth(display, overlay->screen));
    if (pixmap == 0) {
        return 0;
    }
    GC gc = XCreateGC(display, pixmap, 0, NULL);
    if (gc == NULL) {
        XFreePixmap(display, pixmap);
        return 0;
    }
    XSetForeground(display, gc, bg);
    XFillRectangle(display, pixmap, gc, 0, 0,
                   overlay->width, overlay->height);
    XSetForeground(display, gc, fg);
    if (overlay->font != NULL) {
        XSetFont(display, gc, overlay->font->fid);
    }
    XDrawString(display, pixmap, gc, 16, 32, text, (int)strlen(text));
    XFreeGC(display, gc);

    if (overlay->background != 0) {
        XFreePixmap(display, overlay->background);
    }
    overlay->background = pixmap;
    XSetWindowBackgroundPixmap(display, overlay->window, pixmap);
    XMapRaised(display, overlay->window);
    XClearWindow(display, overlay->window);
    XFlush(display);
    return 1;
}

static void eloqi_overlay_hide(EloqiOverlay *overlay) {
    if (overlay == NULL || overlay->display == NULL) {
        return;
    }
    XUnmapWindow(overlay->display, overlay->window);
    XFlush(overlay->display);
}

static void eloqi_overlay_destroy(EloqiOverlay *overlay) {
    if (overlay == NULL) {
        return;
    }
    if (overlay->display != NULL) {
        if (overlay->background != 0) {
            XFreePixmap(overlay->display, overlay->background);
        }
        if (overlay->font != NULL) {
            XFreeFont(overlay->display, overlay->font);
        }
        if (overlay->window != 0) {
            XDestroyWindow(overlay->display, overlay->window);
        }
        XCloseDisplay(overlay->display);
    }
    free(overlay);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/xiangchang24/eloqi/internal/platform"
)

type x11Overlay struct {
	mu     sync.Mutex
	native *C.EloqiOverlay
	closed bool
}

var _ platform.Overlay = (*x11Overlay)(nil)

func newX11Overlay() (*x11Overlay, error) {
	if !ensureX11Threads() {
		return nil, errors.New("x11 overlay: Xlib thread initialization failed")
	}
	native := C.eloqi_overlay_create()
	if native == nil {
		return nil, errors.New("x11 overlay: cannot create window (is DISPLAY set?)")
	}
	return &x11Overlay{native: native}, nil
}

func (o *x11Overlay) Show(state platform.OverlayState, message string) error {
	text, err := overlayDisplayText(state, message)
	if err != nil {
		return err
	}
	color := overlayColor(state)

	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return errors.New("x11 overlay: closed")
	}
	cText := C.CString(text)
	cColor := C.CString(color)
	defer C.free(unsafe.Pointer(cText))
	defer C.free(unsafe.Pointer(cColor))
	if C.eloqi_overlay_show(o.native, cColor, cText) == 0 {
		return fmt.Errorf("x11 overlay: draw %s state", state)
	}
	return nil
}

func (o *x11Overlay) Hide() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil
	}
	C.eloqi_overlay_hide(o.native)
	return nil
}

func (o *x11Overlay) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil
	}
	C.eloqi_overlay_destroy(o.native)
	o.native = nil
	o.closed = true
	return nil
}

func overlayColor(state platform.OverlayState) string {
	switch state {
	case platform.OverlayConnecting:
		return "#36558f"
	case platform.OverlayRecording:
		return "#b42318"
	case platform.OverlayStopping:
		return "#9a6700"
	case platform.OverlayWaiting:
		return "#175cd3"
	case platform.OverlayError:
		return "#b42318"
	default:
		return "#24292f"
	}
}
