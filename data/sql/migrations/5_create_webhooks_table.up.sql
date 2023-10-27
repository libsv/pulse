CREATE TABLE webhooks(
    url                 VARCHAR(255) PRIMARY KEY
    ,tokenHeader        VARCHAR(255)
    ,token              VARCHAR(255)
    ,createdAt          TIMESTAMP DEFAULT CURRENT_TIMESTAMP
    ,lastEmitStatus     VARCHAR(255)
    ,lastEmitTimestamp  TIMESTAMP
    ,errorsCount        INTEGER DEFAULT 0
    ,active             BOOLEAN DEFAULT TRUE
);
