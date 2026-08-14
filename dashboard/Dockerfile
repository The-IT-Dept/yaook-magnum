# Yaook's Horizon image with the Magnum dashboard panels added.
#
# Yaook's HorizonDeployment CRD has no image override (the horizon-operator
# pins the image from targetRelease), and Yaook's Horizon ships only the
# Designate and Octavia dashboards. This image is the same Horizon with
# magnum-ui installed, so it can be run as an additional dashboard.
ARG HORIZON_IMAGE=registry.yaook.cloud/yaook/horizon-2025.1:2.1.142
FROM ${HORIZON_IMAGE}

USER root

# Match the OpenStack release of the base image (2025.1 -> magnum-ui 16.x).
ARG MAGNUM_UI_VERSION=">=16.0.0,<17.0.0"
RUN pip3 install --no-cache-dir "magnum-ui${MAGNUM_UI_VERSION}" python-magnumclient

# Apache serves Horizon out of a source checkout (see the WSGIScriptAlias in
# /etc/apache2/conf-available/horizon.conf), NOT out of site-packages. Panels
# registered under site-packages are silently ignored, so the enabled files and
# the static assets have to be installed into that tree. This is the same place
# Yaook puts the Designate and Octavia panels.
RUN set -eux; \
    HORIZON_DIR="$(sed -nE 's#.*WSGIScriptAlias\s+/\s+(/.*)/openstack_dashboard/wsgi/.*#\1#p' \
        /etc/apache2/conf-available/horizon.conf | head -1)"; \
    test -n "$HORIZON_DIR"; \
    test -d "$HORIZON_DIR/openstack_dashboard/local/enabled"; \
    echo "horizon source dir: $HORIZON_DIR"; \
    SP="$(python3 -c 'import magnum_ui, os; print(os.path.dirname(magnum_ui.__file__))')"; \
    cp "$SP"/enabled/_[0-9]*.py "$HORIZON_DIR"/openstack_dashboard/local/enabled/; \
    ls "$HORIZON_DIR"/openstack_dashboard/local/enabled/ | grep container_infra; \
    echo "$HORIZON_DIR" > /etc/horizon-dir

# Rebuild the static assets so the new panels' JS/CSS are served.
#
# Both passes are required. collectstatic publishes the new panels' assets, and
# compress regenerates django-compressor's OFFLINE MANIFEST. Horizon runs with
# COMPRESS_OFFLINE enabled, and adding a dashboard changes the SCSS bundle, so
# without the compress pass every page (including the login page) dies with
# "OfflineGenerationError: ... key is missing from offline manifest".
#
# The dummy values only satisfy settings during the build; the real ones come
# from the environment at runtime.
RUN set -eux; \
    HORIZON_DIR="$(cat /etc/horizon-dir)"; \
    cd "$HORIZON_DIR"; \
    export YAOOK_HORIZON_KEYSTONE_URL=http://localhost:5000/v3; \
    export YAOOK_HORIZON_INTERFACE=internal; \
    export YAOOK_HORIZON_SECRET_KEY=build-time-only; \
    export YAOOK_HORIZON_POLICY_FILES={}; \
    python3 manage.py collectstatic --noinput > /tmp/collect.log; \
    tail -1 /tmp/collect.log; \
    python3 manage.py compress --force > /tmp/compress.log 2>&1 || { tail -30 /tmp/compress.log; exit 1; }; \
    tail -2 /tmp/compress.log; \
    test -f "$HORIZON_DIR/static/CACHE/manifest.json" || \
      find "$HORIZON_DIR/static" -name "manifest.json" -print -quit; \
    rm -f /tmp/collect.log /tmp/compress.log

# Yaook's Horizon image ships an empty LOGGING config, so tracebacks are
# swallowed and never reach the container log. Send them to stdout.
COPY _90_logging.py /tmp/_90_logging.py
RUN set -eux; \
    HORIZON_DIR="$(cat /etc/horizon-dir)"; \
    install -D -m 0644 /tmp/_90_logging.py "$HORIZON_DIR/openstack_dashboard/local/local_settings.d/_90_logging.py"; \
    rm -f /tmp/_90_logging.py; \
    echo "logging config installed"
