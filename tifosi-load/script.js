import http from "k6/http";
import { sleep, check } from "k6";

export const options = {
  scenarios: {
    ferrari_is_losing: {
      executor: "ramping-vus",
      startVUs: 0,
      stages: [
        { duration: "10s", target: 10 },
        { duration: "10s", target: 0 },
      ],
      gracefulRampDown: "0s",
    },
    ferrari_is_winning: {
      executor: "ramping-vus",
      startVUs: 10,
      stages: [
        { duration: "10s", target: 10 },
        { duration: "2s", target: 100 },
        { duration: "10s", target: 10 },
      ],
    },
  },
};

export default function () {
  const response = makeAPizzaRequest();
}

function makeAPizzaRequest() {
  const paddock = __ENV.PADDOCK_GATEWAY || "http://localhost:8080";
  const orderEndpoint = `${paddock}/v1/order`;
  const headers = { "Content-Type": "application/json" };
  const body = JSON.stringify({
    size: "large",
    username: "tubias",
    destination: "manoel moreira",
  });
  let res = http.post(orderEndpoint, body, { headers: headers });
  check(res, { "status is 200": (res) => res.status === 200 });
  sleep(1);

  const jsonResponse = res.json();
  return jsonResponse;
}
