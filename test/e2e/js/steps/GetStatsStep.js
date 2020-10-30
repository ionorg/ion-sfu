const {TestUtils, TestStep} = require('kite-common');

/**
 * Class: GetStapStep
 * Extends: TestStep
 * Description:
 */
class GetStatsStep extends TestStep {
  constructor(kiteBaseTest) {
    super();
    this.driver = kiteBaseTest.driver;
    this.statsCollectionTime = kiteBaseTest.statsCollectionTime;
    this.statsCollectionInterval = kiteBaseTest.statsCollectionInterval;
    this.peerConnections = kiteBaseTest.peerConnections;
    this.selectedStats = kiteBaseTest.selectedStats;

    // Test reporter if you want to add attachment(s)
    this.testReporter = kiteBaseTest.reporter;
  }

  stepDescription() {
    return "Get the peer connection's stats";
  }

  async step() {
    try {
      let stats = await TestUtils.getStats(this, 'kite', this.peerConnections);
  
      let summaryStats = await TestUtils.extractJson(stats, 'both');
  
      // Data
      this.testReporter.textAttachment(this.report, 'Raw stats', JSON.stringify(stats, null, 4), "json");
      this.testReporter.textAttachment(this.report, 'Summary stats', JSON.stringify(summaryStats, null, 4), "json");
      
      } catch (error) {
        console.log(error);
        throw new KiteTestError(Status.BROKEN, "Failed to getStats");
      }
  }
}

module.exports = GetStatsStep;
