FROM ubuntu:groovy

ENV DEBIAN_FRONTEND noninteractive

USER root
RUN apt update && \
  apt install -y --no-install-recommends \
  curl \
  dirmngr \
  apt-transport-https \
  lsb-release \
  ca-certificates \
  default-jre \
  default-jdk \
  openjdk-8-jdk \
  maven \
  git \
  gcc \
  build-essential \
  nano \
  wget \
  dpkg \
  unzip \
  xvfb \
  nodejs \
  fonts-liberation \
  libappindicator3-1 \
  libgbm1 \
  libgtk-3-0 \
  libxss1 \
  xdg-utils \
  keyboard-configuration \
  nmap \
  sudo \
  xterm
RUN apt -y update
RUN apt -y install npm

WORKDIR /

# KITE
RUN git clone https://github.com/webrtc/KITE.git KITE

ENV KITE_HOME=/KITE
RUN JAVA_HOME=/usr/lib/jvm/java-8-openjdk-amd64/bin
ENV MAVEN_HOME=/usr/share/maven

WORKDIR $KITE_HOME

RUN chmod +x *.sh ./configureLinux.sh

RUN echo 'debconf debconf/frontend select Noninteractive' | debconf-set-selections
RUN yes | $KITE_HOME/configureLinux.sh

RUN $KITE_HOME/scripts/linux/path/c

COPY entrypoint.sh .
RUN chmod +x entrypoint.sh

ENTRYPOINT ["/KITE/entrypoint.sh"]
