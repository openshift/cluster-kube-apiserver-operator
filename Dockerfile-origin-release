FROM registry.svc.ci.openshift.org/openshift/origin-release:v4.0 as origin-release

FROM centos:7 as jq
ARG IMAGES
ARG IMAGE_ORG
RUN curl -sLk https://github.com/stedolan/jq/releases/download/jq-1.5/jq-linux64 -o /tmp/jq-linux64 && \
    cp /tmp/jq-linux64 /usr/bin/jq && \
    chmod +x /usr/bin/jq && \
    rm -f /tmp/jq-linux64

COPY --from=origin-release release-manifests/image-references .
RUN jq '.spec.tags |= map(.name as $name | if (['$IMAGES'] | index($name)) then ("'$IMAGE_ORG'/origin-"+$name+":latest") as $override | ("Switching \($name): \(.from.name) => \($override)" | stderr) as $stderr | .from.name |= $override else . end)' image-references > image-references-updated
RUN diff -u image-references image-references-updated || true

FROM registry.svc.ci.openshift.org/openshift/origin-release:v4.0
COPY --from=jq image-references-updated release-manifests/image-references
