FROM ubuntu:16.04
WORKDIR /work
RUN apt-get update && apt-get install -y \
  libssl-dev ssl-cert php7.0 libapache2-mod-php7.0 \
  wget unzip php7.0-mcrypt libmcrypt-dev \
  && apt-get clean && rm -rf /var/lib/apt/lists/*
RUN wget https://github.com/FogNo23/DNASrep/archive/master.zip \
  && unzip master.zip \
  && mv DNASrep-master DNASrep \
  && rm master.zip
RUN echo "extension=mcrypt.so" > /etc/php/7.0/mods-available/mcrypt.ini \
  && a2enmod ssl && a2ensite default-ssl && phpenmod mcrypt
RUN cp -r /work/DNASrep/www/dnas /var/www/dnas \
  && cp -r /work/DNASrep/etc/dnas /etc/dnas \
  && ln -s /etc/apache2/sites-available/dnas.conf /etc/apache2/sites-enabled/dnas.conf \
  && unlink /etc/apache2/sites-enabled/000-default.conf \
  && unlink /etc/apache2/sites-enabled/default-ssl.conf \
  && ln -sf /proc/self/fd/1 /var/log/apache2/access.log \
  && ln -sf /proc/self/fd/1 /var/log/apache2/error.log
ADD ./dnas.conf /etc/apache2/sites-available/dnas.conf

EXPOSE 443
CMD ["apachectl", "-D", "FOREGROUND"]