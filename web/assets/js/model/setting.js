class AllSetting {

    constructor(data) {
        this.webListen = "";
        this.webDomain = "";
        this.webPort = 2053;
        this.webCertFile = "";
        this.webKeyFile = "";
        this.webBasePath = "/";
        this.sessionMaxAge = 360;
        this.pageSize = 25;
        this.expireDiff = 0;
        this.trafficDiff = 0;
        this.remarkModel = "-ieo";
        this.datepicker = "gregorian";
        this.tgBotEnable = false;
        this.tgBotToken = "";
        this.tgBotProxy = "";
        this.tgBotAPIServer = "";
        this.tgBotChatId = "";
        this.tgRunTime = "@daily";
        this.tgBotBackup = false;
        this.tgBotLoginNotify = true;
        this.tgCpu = 80;
        this.tgLang = "en-US";
        this.twoFactorEnable = false;
        this.twoFactorToken = "";
        this.xrayTemplateConfig = "";
        this.subEnable = true;
        this.subJsonEnable = false;
        this.subTitle = "";
        this.subSupportUrl = "";
        this.subProfileUrl = "";
        this.subAnnounce = "";
        this.subEnableRouting = true;
        this.subRoutingRules = "";
        this.subListen = "";
        this.subPort = 2096;
        this.subPath = "/sub/";
        this.subJsonPath = "/json/";
        this.subClashEnable = true;
        this.subClashPath = "/clash/";
        this.subDomain = "";
        this.externalTrafficInformEnable = false;
        this.externalTrafficInformURI = "";
        this.subCertFile = "";
        this.subKeyFile = "";
        this.subUpdates = 12;
        this.subEncrypt = true;
        this.subShowInfo = true;
        this.subURI = "";
        this.subJsonURI = "";
        this.subClashURI = "";
        this.subJsonFragment = "";
        this.subJsonNoises = "";
        this.subJsonMux = "";
        this.subJsonRules = "";

        this.timeLocation = "Local";

        // LDAP settings
        this.ldapEnable = false;
        this.ldapHost = "";
        this.ldapPort = 389;
        this.ldapUseTLS = false;
        this.ldapBindDN = "";
        this.ldapPassword = "";
        this.ldapBaseDN = "";
        this.ldapUserFilter = "(objectClass=person)";
        this.ldapUserAttr = "mail";
        this.ldapVlessField = "vless_enabled";
        this.ldapSyncCron = "@every 1m";
        this.ldapFlagField = "";
        this.ldapTruthyValues = "true,1,yes,on";
        this.ldapInvertFlag = false;
        this.ldapInboundTags = "";
        this.ldapAutoCreate = false;
        this.ldapAutoDelete = false;
        this.ldapDefaultTotalGB = 0;
        this.ldapDefaultExpiryDays = 0;
        this.ldapDefaultLimitIP = 0;
        this.apiKey = "";

        // 流量商店
        this.shopEnable = false;
        this.shopPlans = "";
        this.shopPayNote = "";
        this.shopQrUrl = "";
        this.shopQrUrl2 = "";
        this.shopPayMode = "manual";
        this.shopBufAid = "";
        this.shopBufSecret = "";
    this.shopNotifyHTTPPort = 0;
    this.shopPendingTTL = 3;
    this.shopUsageNotify = true;
    this.mailBackupEnable = false;
    this.mailBackupTo = "";
    this.mailSMTPHost = "";
    this.mailSMTPPort = 465;
    this.mailSMTPUser = "";
    this.mailSMTPPass = "";
    this.mailSMTPFrom = "";
    this.mailSMTPInsecure = false;
    this.mailBackupCompress = false;
    this.mailBackupEveryDays = 1;
        this.shopInboundId = 0;
        this.shopDailyLimit = 3;
        this.shopLimitIp = 2;

        // 流量重置默认值（不声明的话，在「设置」页保存时会被后端写成空值）
        this.defaultResetMethod = "never";
        this.defaultResetDay = 1;

        // 数据库备份（同理：漏声明就会被清空，表现为「自动备份老是自己关掉」）
        this.dbBackupEnable = false;
        this.dbBackupPath = "/etc/x-ui/backups/";
        this.dbBackupRetention = 30;

        // Xray 定时重启
        this.xrayRestartEnable = false;
        this.xrayRestartCron = "@every 6h";

        // URI 随机化
        this.uriRandomizeEnable = false;

        // VPS 配置（漏声明会被「设置」页保存时清空 —— 就是「VPS 配置老是被清空」的元凶）
        this.vpsName = "";
        this.vpsIP = "";
        this.vpsLocation = "";
        this.vpsSpecs = "";
        this.vpsPurchaseDate = "";
        this.vpsNodeInfo = "";
        this.vpsNotes = "";
        this.vpsResetDay = 0;
        this.vpsQuotaGB = 0;
        this.vpsQuotaNotify = "80,95";
        this.vpsQuotaNotified = "";

        // 三网延迟监测
        this.latencyEnable = true;
        this.latencyInterval = 5;
        this.latencyRetentionDays = 30;
        this.latencyTargets = "";
        // 订阅防护
        this.subGuardEnable = false;
        this.subGuardStrictUA = false;
        this.subGuardRPM = 30;
        this.subGuardBlockMin = 30;

        if (data == null) {
            return
        }
        ObjectUtil.cloneProps(this, data);
        // cloneProps 只拷贝「本类已声明」的字段。这里再兜一次底：
        // 后端以后新增设置项时，不会因为前端没声明而在保存时被写成零值。
        for (const key of Object.keys(data)) {
            if (!this.hasOwnProperty(key) && data[key] !== undefined) {
                this[key] = data[key];
            }
        }
    }

    equals(other) {
        return ObjectUtil.equals(this, other);
    }
}