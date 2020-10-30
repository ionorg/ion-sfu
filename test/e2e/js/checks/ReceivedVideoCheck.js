const { TestStep, KiteTestError, Status } = require("kite-common");

class ReceivedVideoCheck extends TestStep {
  constructor(kiteBaseTest) {
    super();
    this.driver = kiteBaseTest.driver;
    this.timeout = kiteBaseTest.timeout;
    this.numberOfParticipant = kiteBaseTest.numberOfParticipant;
    this.page = kiteBaseTest.page;

    // Test reporter if you want to add attachment(s)
    this.testReporter = kiteBaseTest.reporter;
  }

  stepDescription() {
    return "Check the other videos are being received OK";
  }

  async step() {
    let result = "";
    let tmp;
    let error = false;
    try {
      for (let i = 1; i < this.numberOfParticipant; i++) {
        tmp = await this.page.videoCheck(this, i);
        result += tmp;
        if (i < this.numberOfParticipant) {
          result += " | ";
        }
        if (tmp != "video") {
          error = true;
        }
      }
      if (error) {
        this.testReporter.textAttachment(
          this.report,
          "Received videos",
          result,
          "plain"
        );
        throw new KiteTestError(
          Status.FAILED,
          "Some videos are still or blank: " + result
        );
      }
    } catch (error) {
      console.log(error);
      if (error instanceof KiteTestError) {
        throw error;
      } else {
        throw new KiteTestError(Status.BROKEN, "Error looking for the video");
      }
    }
  }
}

module.exports = ReceivedVideoCheck;
