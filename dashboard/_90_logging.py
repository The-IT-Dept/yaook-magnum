# Yaook's Horizon image ships an empty LOGGING config, which means unhandled
# exceptions are swallowed and never reach the container log. Route them to
# stdout so `kubectl logs` is useful.
LOGGING = {
    "version": 1,
    "disable_existing_loggers": False,
    "formatters": {"plain": {"format": "%(asctime)s %(levelname)s %(name)s %(message)s"}},
    "handlers": {
        "console": {"class": "logging.StreamHandler", "level": "INFO", "formatter": "plain"},
    },
    "loggers": {
        "django.request": {"handlers": ["console"], "level": "ERROR", "propagate": False},
        "django": {"handlers": ["console"], "level": "WARNING", "propagate": False},
        "horizon": {"handlers": ["console"], "level": "INFO", "propagate": False},
        "openstack_dashboard": {"handlers": ["console"], "level": "INFO", "propagate": False},
        "magnum_ui": {"handlers": ["console"], "level": "INFO", "propagate": False},
        "keystoneauth": {"handlers": ["console"], "level": "WARNING", "propagate": False},
    },
}
