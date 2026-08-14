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

# Register the Magnum panels with Horizon.
RUN set -eux; \
    SP="$(python3 -c 'import openstack_dashboard, os; print(os.path.dirname(os.path.dirname(openstack_dashboard.__file__)))')"; \
    cp "$SP"/magnum_ui/enabled/_*.py "$SP"/openstack_dashboard/local/enabled/; \
    python3 -c "import magnum_ui; print('magnum_ui', magnum_ui.__name__, 'importable')"; \
    ls "$SP"/openstack_dashboard/local/enabled/ | grep container_infra

# Rebuild the static assets so the new panels' JS/CSS are served. Horizon's
# STATIC_ROOT is populated at image build time, so adding a panel requires
# re-running collectstatic. COMPRESS_OFFLINE is False in this image, so
# django-compressor does not need a separate pass.
#
# The dummy values only satisfy local_settings during the build; the real ones
# come from the environment at runtime.
RUN set -eux; \
    printf '%s\n' \
      'import os, sys' \
      'from django.core.management import execute_from_command_line' \
      'os.environ.setdefault("DJANGO_SETTINGS_MODULE", "openstack_dashboard.settings")' \
      'execute_from_command_line(sys.argv)' > /usr/local/bin/horizon-manage.py; \
    YAOOK_HORIZON_KEYSTONE_URL=http://localhost:5000/v3 \
    YAOOK_HORIZON_INTERFACE=internal \
    YAOOK_HORIZON_SECRET_KEY=build-time-only \
    YAOOK_HORIZON_POLICY_FILES={} \
    python3 /usr/local/bin/horizon-manage.py collectstatic --noinput | tail -1
