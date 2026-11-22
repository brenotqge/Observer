"use strict";

document.querySelectorAll("[data-test-api]").forEach((button) => {
  button.addEventListener("click", async () => {
    const api = button.dataset.testApi;
    const status = button.parentElement.querySelector(".test-status");
    const paths = ["/", "/", api, api, "/slow", "/slow", "/missing", "/error"];
    const durationSeconds = 30;
    let total = 0;
    let success = 0;
    let problems = 0;
    let finished = false;

    button.disabled = true;
    const startedAt = Date.now();

    const sendBatch = async () => {
      const batch = Array.from({ length: 6 }, (_, index) => {
        const path = paths[(total + index) % paths.length];
        return fetch(path, { cache: "no-store" })
          .then(async (response) => {
            await response.text();
            total++;
            if (response.status >= 200 && response.status < 400) success++;
            else problems++;
          })
          .catch(() => {
            total++;
            problems++;
          });
      });
      await Promise.all(batch);
      if (!finished) {
        status.textContent = `Teste em andamento - ${Math.max(0, durationSeconds - Math.floor((Date.now() - startedAt) / 1000))} segundos restantes - ${total} acessos`;
      }
    };

    const countdown = setInterval(() => {
      if (finished) return;
      const elapsed = Math.floor((Date.now() - startedAt) / 1000);
      const remaining = Math.max(0, durationSeconds - elapsed);
      status.textContent = `Teste em andamento - ${remaining} segundos restantes - ${total} acessos`;
    }, 500);

    const traffic = setInterval(sendBatch, 250);
    sendBatch();

    setTimeout(() => {
      finished = true;
      clearInterval(traffic);
      clearInterval(countdown);
      status.textContent = `Teste concluído em 30 segundos: ${total} acessos gerados - ${success} respostas normais e ${problems} respostas com problema.`;
      button.disabled = false;
    }, durationSeconds * 1000);
  });
});
