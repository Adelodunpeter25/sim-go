// Package slim is sim-go's fixed slim profile.
//
// Borrowed idea from reference/simslim (profiles.go): a simulator boots ~180
// background daemons (Siri, Spotlight, iCloud, photo analysis, ...). Disabling
// them cuts memory ~4x (4.0 GB -> 0.9 GB phys_footprint).
//
// Unlike simslim, v1 is deliberately NOT configurable: one fixed Safe set that
// keeps dev-critical services running (push/apsd, StoreKit, universal-links/
// swcd, contacts/calendar pickers, sharingd). If you need those background
// features, run `sim-go restore`.
package slim

// IOSLabels is the fixed set of launchd labels `ios.Slim` disables via:
//
//	xcrun simctl spawn <udid> launchctl disable system/<label>
//	xcrun simctl spawn <udid> launchctl bootout system/<label>
//
// Sourced from reference/simslim/profiles.go categories:
//   - search (Spotlight): 5 labels, breaks Settings search only
//   - icloud (sync/account): 22 labels
//   - siri (assistant/intelligence): 29 labels
//   - widgets (posters/live activities): 3 labels, biggest single win (~675MB)
//   - telemetry (ads/diagnostics): 11 labels
//   - photos analysis: 8 labels (picker may act up, documented)
//   - family, health, apps(news/weather/maps/games), messaging, connectivity extras
var IOSLabels = []string{
	// search
	"com.apple.searchd",
	"com.apple.searchtoold",
	"com.apple.spotlightknowledged",
	"com.apple.spotlightknowledged.updater",
	"com.apple.corespotlightservice",

	// icloud & apple account
	"com.apple.appleaccountd",
	"com.apple.appleaccounttransparencyd",
	"com.apple.appleidsetupd",
	"com.apple.akd",
	"com.apple.amsaccountsd",
	"com.apple.amsengagementd",
	"com.apple.amsondevicestoraged",
	"com.apple.cloudd",
	"com.apple.cloudphotod",
	"com.apple.ckdiscretionaryd",
	"com.apple.cloudsettingssyncagent",
	"com.apple.bird",
	"com.apple.syncdefaultsd",
	"com.apple.cdpd",
	"com.apple.sosd",
	"com.apple.SecureBackupDaemon",
	"com.apple.TrustedPeersHelper",
	"com.apple.protectedcloudstorage.protectedcloudkeysyncing",
	"com.apple.icloudmailagent",
	"com.apple.icloudsubscriptionoptimizerd",
	"com.apple.communicationtrustd",

	// siri & intelligence
	"com.apple.assistantd",
	"com.apple.assistant_cdmd",
	"com.apple.assistant_service",
	"com.apple.siriactionsd",
	"com.apple.siriinferenced",
	"com.apple.siriknowledged",
	"com.apple.sirittsd",
	"com.apple.siri.context.service",
	"com.apple.siri.acousticsignature",
	"com.apple.corespeechd",
	"com.apple.voiced",
	"com.apple.voicebankingd",
	"com.apple.speechmodeltrainingd",
	"com.apple.intelligenceplatformd",
	"com.apple.intelligencecontextd",
	"com.apple.intelligenceflowd",
	"com.apple.intelligencetasksd",
	"com.apple.generativeexperiencesd",
	"com.apple.knowledgeconstructiond",
	"com.apple.naturallanguaged",
	"com.apple.textunderstandingd",
	"com.apple.modelcatalogd",
	"com.apple.modelmanagerd",
	"com.apple.mlhostd",
	"com.apple.mlruntimed",
	"com.apple.suggestd",
	"com.apple.parsecd",
	"com.apple.parsec-fbf",
	"com.apple.proactiveeventtrackerd",

	// widgets & wallpaper (largest single saving)
	"com.apple.PosterBoard",
	"com.apple.chronod",
	"com.apple.liveactivitiesd",

	// telemetry
	"com.apple.ap.adprivacyd",
	"com.apple.ap.promotedcontentd",
	"com.apple.diagnosticextensionsd",
	"com.apple.feedbackd",
	"com.apple.rtcreportingd",
	"com.apple.securityuploadd",
	"com.apple.geoanalyticsd",
	"com.apple.triald",
	"com.apple.followupd",
	"com.apple.purplebuddy.budd",
	"com.apple.devicecheckd",

	// photos & media analysis
	"com.apple.photoanalysisd",
	"com.apple.photosface",
	"com.apple.mediaanalysisd",
	"com.apple.mediaanalysisd.service",
	"com.apple.mediastream.mstreamd",
	"com.apple.medialibraryd",
	"com.apple.assetsd",
	"com.apple.assetsd.nebulad",

	// family & screen time
	"com.apple.familycircled",
	"com.apple.FamilyControlsAgent",
	"com.apple.familynotification",
	"com.apple.askpermissiond",
	"com.apple.asktod",
	"com.apple.ScreenTimeAgent",
	"com.apple.ScreenTimeSettingsAgent",
	"com.apple.UsageTrackingAgent",

	// health/home/fitness
	"com.apple.healthd",
	"com.apple.healthappd",
	"com.apple.healthcontentd",
	"com.apple.healtheventsd",
	"com.apple.healthrecordsd",
	"com.apple.finhealthd",
	"com.apple.homed",
	"com.apple.homeeventsd",
	"com.apple.fitcore",
	"com.apple.fitcore.session",
	"com.apple.fitnesscoachingd",
	"com.apple.fitnessintelligenced",
	"com.apple.activityawardsd",
	"com.apple.activitysharingd",

	// news/weather/maps/games
	"com.apple.newsd",
	"com.apple.weatherd",
	"com.apple.Maps.mapssyncd",
	"com.apple.Maps.mapspushd",
	"com.apple.Maps.geocorrectiond",
	"com.apple.maps.destinationd",
	"com.apple.MapKit.SnapshotService",
	"com.apple.jetpackassetd",
	"com.apple.tipsd",
	"com.apple.gamed",
	"com.apple.gamesaved",
	"com.apple.GameController.gamecontrollerd",

	// messaging/facetime
	"com.apple.identityservicesd",
	"com.apple.ids_simd",
	"com.apple.imautomatichistorydeletionagent",
	"com.apple.imcore.imtransferagent",
	"com.apple.imdpersistence.IMDPersistenceAgent",
	"com.apple.facetimemessagestored",
	"com.apple.telephonyutilities.callservicesd",
}

// Deliberately NOT slimmed (dev-critical, cf. simslim README "What you lose"):
//   - com.apple.apsd (push), com.apple.storekitd (StoreKit testing)
//   - com.apple.swcd (universal links), com.apple.sharingd (share sheets)
//   - mail/calendar/contacts, safari sync, wallet/finance, connectivity.

// AndroidPackages is the fixed set `android.Slim` disables via:
//
//	adb -s <serial> shell pm disable-user --user 0 <pkg>
//
// Mirrors avdslim (~45 background packages) as seen in reference/simfleet.
// System-critical packages (launcher, settings, gms core) are never touched.
var AndroidPackages = []string{
	"com.google.android.apps.maps",
	"com.google.android.apps.photos",
	"com.google.android.apps.youtube.music",
	"com.google.android.videos",
	"com.google.android.music",
	"com.google.android.apps.docs",
	"com.google.android.apps.messaging",
	"com.google.android.apps.dialer",
	"com.google.android.contacts",
	"com.google.android.calendar",
	"com.google.android.gm",
	"com.google.android.apps.tachyon", // Duo/Meet
	"com.android.chrome",
	"com.android.vending", // Play Store (re-enable with restore for store tests)
	"com.android.providers.downloads.ui",
	"com.google.android.feedback",
	"com.google.android.onetimeinitializer",
	"com.google.android.apps.wellbeing",
	"com.google.android.apps.safetyhub",
	"com.google.android.apps.subscriptions.red",
	"com.android.wallpaper.livepicker",
	"com.android.dreams.basic",
	"com.android.bookmarkprovider",
	"com.google.android.printservice.recommendation",
	"com.google.android.tts",
	"com.android.hotwordenrollment.okgoogle",
	"com.android.hotwordenrollment.xgoogle",
	"com.google.android.googlequicksearchbox",
	"com.google.android.apps.googleassistant",
	"com.google.android.speech",
	"com.google.android.voicesearch",
	"com.android.bips",
	"com.google.android.marvin.talkback",
	"com.google.android.accessibility.auditor",
	"com.google.android.projection.gearhead",
	"com.google.android.apps.car",
	"com.google.android.apps.healthconnect",
	"com.fitbit.FitbitMobile",
	"com.google.android.apps.nbu.files",
	"com.google.android.apps.cloudconsole",
	"com.android.egg",
	"com.android.traceur",
}
