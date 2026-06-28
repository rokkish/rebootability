# scratch ベース: 5MB 弱の極小イメージ。
# tinyserver は static binary なので glibc 不要。
FROM scratch
COPY tinyserver /tinyserver
EXPOSE 18080
ENV LISTEN_ADDR=:18080
ENTRYPOINT ["/tinyserver"]
